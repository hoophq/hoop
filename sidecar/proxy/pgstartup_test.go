package proxy

import (
	"encoding/binary"
	"maps"
	"strings"
	"testing"
	"unicode/utf8"
)

// pgStartupParams builds a v3 StartupMessage from key/value pairs, in order,
// repeats included.
func pgStartupParams(kv ...string) []byte {
	var params []byte
	for _, s := range kv {
		params = append(params, s...)
		params = append(params, 0)
	}
	params = append(params, 0)
	out := make([]byte, 8, 8+len(params))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(params)))
	binary.BigEndian.PutUint32(out[4:8], 3<<16)
	return append(out, params...)
}

// The value recorded must be the value the backend runs with. Every case is
// one the backend reads a particular way (pg_split_opts, then getopt over the
// server's switches), and a relay reading it differently records a trace id
// the session never had.
func TestOptionSettingsReadTheWayTheBackendDoes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options string
		want    map[string]string
	}{
		{"-c with a separate word", "-c claude.session.id=xyz1234678",
			map[string]string{"claude.session.id": "xyz1234678"}},
		{"-c run together", "-cclaude.session.id=xyz",
			map[string]string{"claude.session.id": "xyz"}},
		{"long form", "--claude.session.id=xyz",
			map[string]string{"claude.session.id": "xyz"}},
		{"names fold case and dashes", "-c Claude.Session-Id=xyz",
			map[string]string{"claude.session_id": "xyz"}},
		{"backslash escapes a space", `-c claude.note=two\ words`,
			map[string]string{"claude.note": "two words"}},
		{"escaped backslash", `-c claude.path=a\\b`,
			map[string]string{"claude.path": `a\b`}},
		{"tabs and newlines separate", "-c\ta.b=1\n-c a.c=2",
			map[string]string{"a.b": "1", "a.c": "2"}},
		{"value keeps an equals sign", "-c a.b=k=v",
			map[string]string{"a.b": "k=v"}},
		{"empty value is a value", "-c a.b=",
			map[string]string{"a.b": ""}},
		// -d takes an argument. Reading "5" as a word of its own would
		// desynchronize everything after it.
		{"other switches keep their argument", "-d 5 -c a.b=1",
			map[string]string{"a.b": "1"}},
		{"a cluster ending in c", "-Ec a.b=1",
			map[string]string{"a.b": "1"}},
		{"last assignment wins", "-c a.b=1 -c a.b=2",
			map[string]string{"a.b": "2"}},
		// Each of these makes the backend refuse the connection, so nothing
		// after the failure point can govern a statement.
		{"stops at a non-switch word", "junk -c a.b=1", map[string]string{}},
		{"stops at --", "-- -c a.b=1", map[string]string{}},
		{"stops at an unknown switch", "-Z -c a.b=1", map[string]string{}},
		{"stops at -c with no value", "-c a.b -c a.c=1", map[string]string{}},
		{"-c at the end has no argument", "-c a.b=1 -c", map[string]string{"a.b": "1"}},
		{"empty", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pgOptionSettings(tc.options); !maps.Equal(got, tc.want) {
				t.Errorf("pgOptionSettings(%q) = %v, want %v", tc.options, got, tc.want)
			}
		})
	}
}

func TestStartupMetadataLiftsOnlyWhatWasConfigured(t *testing.T) {
	params := map[string]string{
		"user":             "alice",
		"application_name": "claude-code",
		"options":          "-c claude.session.id=xyz1234678 -c search_path=private",
	}
	got := startupMetadata(params, []StartupMetadata{
		{Option: "Claude.Session.Id", Key: "claude.session.id"},
		{Parameter: "application_name"},
		// Configured but not sent: no key, rather than an empty value a
		// policy could not tell from "sent empty".
		{Option: "claude.agent.id", Key: "claude.agent.id"},
	}, false)
	want := map[string]string{
		"claude.session.id":                   "xyz1234678",
		"postgres.parameter.application_name": "claude-code",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("metadata = %v, want %v", got, want)
	}
}

// The default a lane takes with no list: every setting `options` assigns, and
// nothing else from the packet. Keys carry the normalized name under one
// prefix, so a client-chosen name cannot reach a key the relay owns: here
// `principal` becomes postgres.option.principal, never input.context.principal.
func TestAllStartupOptionsRecordsEverySettingUnderItsPrefix(t *testing.T) {
	params := map[string]string{
		"user":             "alice",
		"application_name": "claude-code",
		"options":          "-c claude.session.id=xyz -c Search-Path=private --principal=mallory",
	}
	got := startupMetadata(params, nil, true)
	want := map[string]string{
		"postgres.option.claude.session.id": "xyz",
		"postgres.option.search_path":       "private",
		"postgres.option.principal":         "mallory",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("metadata = %v, want %v", got, want)
	}
	if got := startupMetadata(map[string]string{"user": "alice"}, nil, true); got != nil {
		t.Fatalf("no options recorded %v, want nothing", got)
	}
}

// Narrowing a lane from every option to a list must keep the key a dashboard
// already queries, so a field with no `as` writes what the default wrote.
func TestFieldWithoutKeyWritesTheDefaultKey(t *testing.T) {
	params := map[string]string{"options": "-c Claude.Session-Id=xyz"}
	all := startupMetadata(params, nil, true)
	one := startupMetadata(params, []StartupMetadata{{Option: "claude.session_id"}}, false)
	if !maps.Equal(all, one) {
		t.Fatalf("default wrote %v, the field wrote %v; want the same key", all, one)
	}
	if k := (StartupMetadata{Parameter: "application_name"}).MetadataKey(); k != "postgres.parameter.application_name" {
		t.Fatalf("parameter default key = %q", k)
	}
}

// The value is copied onto every statement record of the session, so a
// client must not be able to multiply an arbitrary blob into the trail. The
// cut keeps valid UTF-8, or a JSON sink writes replacement characters.
func TestStartupMetadataValueIsBounded(t *testing.T) {
	long := strings.Repeat("a", maxStartupMetadataValue-1) + "é" + "tail"
	got := startupMetadata(
		map[string]string{"application_name": long},
		[]StartupMetadata{{Parameter: "application_name", Key: "app"}},
		false,
	)["app"]
	if len(got) > maxStartupMetadataValue {
		t.Fatalf("value is %d bytes, want at most %d", len(got), maxStartupMetadataValue)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated value is not valid UTF-8: %q", got[len(got)-4:])
	}
	if !strings.HasPrefix(long, got) || len(got) < maxStartupMetadataValue-1 {
		t.Fatalf("truncation lost more than the split character: %d bytes kept", len(got))
	}
}

// The value is the client's. A key the policy context owns would let the
// client choose its own principal in every Rego decision.
func TestStartupMetadataRefusesUnusableFields(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field StartupMetadata
		want  string
	}{
		{"reserved key", StartupMetadata{Option: "claude.user", Key: "principal"}, "reserved"},
		{"no source", StartupMetadata{Key: "k"}, "neither"},
		{"two sources", StartupMetadata{Option: "a.b", Parameter: "application_name", Key: "k"}, "both"},
		{"option carries a value", StartupMetadata{Option: "a.b=1", Key: "k"}, "not a setting name"},
		{"option carries a space", StartupMetadata{Option: "a b", Key: "k"}, "not a setting name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.field.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
	if err := validateStartupMetadata([]StartupMetadata{
		{Option: "a.b", Key: "k"}, {Parameter: "application_name", Key: "k"},
	}); err == nil || !strings.Contains(err.Error(), "two fields") {
		t.Fatalf("duplicate key = %v, want refused", err)
	}
}

// ProcessStartupPacket overwrites on each repeat, so the backend logs in the
// LAST user named. An audit trail that kept the first would attribute the
// session to a name the backend never authenticated.
func TestRepeatedStartupUserRecordsTheOneTheBackendAuthenticates(t *testing.T) {
	_, user, _ := negotiateTwo(t, pgStartupParams("user", "alice", "user", "mallory", "database", "appdb"))
	if user != "mallory" {
		t.Fatalf("user = %q, want mallory: the backend authenticates the last one", user)
	}
}
