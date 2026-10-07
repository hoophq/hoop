package services

import (
	"reflect"
	"strings"
	"testing"

	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
)

func sidecarWith(name string, listeners ...daemon.ListenerConfig) *models.Sidecar {
	return &models.Sidecar{
		ID:            "9b2a8b4e-5c0e-4a51-9b7a-1f3e2d4c5b6a",
		OrgID:         "org-1",
		Name:          name,
		Configuration: models.SidecarConfiguration{Listeners: listeners},
	}
}

func TestProjectListenersMapsEveryProtocol(t *testing.T) {
	cases := []struct {
		protocol, typ, subtype string
	}{
		{"postgres", "database", "postgres"},
		{"mysql", "database", "mysql"},
		{"mssql", "database", "mssql"},
		{"mongodb", "database", "mongodb"},
		{"oracle", "database", "oracledb"},
		{"ssh", "application", "ssh"},
		{"http", "httpproxy", "httpproxy"},
		{"clickhouse", "custom", "clickhouse"},
		{"grpc", "custom", "grpc"},
		{"spanner", "custom", "spanner"},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			sc := sidecarWith("pay", daemon.ListenerConfig{Name: "lane", Protocol: tc.protocol})
			got, err := ProjectListeners("org-1", sc)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("want 1 connection, got %d", len(got))
			}
			if got[0].Type != tc.typ || got[0].SubType.String != tc.subtype || !got[0].SubType.Valid {
				t.Errorf("want %s/%s, got %s/%s", tc.typ, tc.subtype, got[0].Type, got[0].SubType.String)
			}
		})
	}
	// The table and the daemon's own list must agree, or a protocol a
	// listener may declare has no mirror.
	for _, p := range daemon.Protocols() {
		if _, err := ProjectListeners("org-1", sidecarWith("pay", daemon.ListenerConfig{Name: "lane", Protocol: p})); err != nil {
			t.Errorf("protocol %q is one a listener may declare but has no mapping: %v", p, err)
		}
	}
}

func TestProjectListenersRendersTheMirror(t *testing.T) {
	sc := sidecarWith("payments",
		daemon.ListenerConfig{Name: "appdb", Protocol: "postgres"},
		daemon.ListenerConfig{Name: "api.v2_beta", Protocol: "http"},
	)
	got, err := ProjectListeners("org-1", sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want one connection per listener, got %d", len(got))
	}
	// Config order, name composition.
	if got[0].Name != "payments-appdb" || got[1].Name != "payments-api.v2_beta" {
		t.Errorf("want names in config order, got %q, %q", got[0].Name, got[1].Name)
	}
	for _, c := range got {
		if c.ID != "" {
			t.Errorf("%s: the ID is the writer's to resolve, got %q", c.Name, c.ID)
		}
		if c.OrgID != "org-1" {
			t.Errorf("%s: org_id=%q", c.Name, c.OrgID)
		}
		if c.ManagedBy.String != models.ConnectionManagedBySidecar || !c.ManagedBy.Valid {
			t.Errorf("%s: managed_by=%+v", c.Name, c.ManagedBy)
		}
		if !c.IsSidecarBacked() || c.SidecarID.String != sc.ID {
			t.Errorf("%s: sidecar_id=%+v", c.Name, c.SidecarID)
		}
		if c.AgentID.Valid {
			t.Errorf("%s: a mirror has no agent, got %+v", c.Name, c.AgentID)
		}
		// The gateway has no route to a sidecar: a mode left on would offer
		// a session that cannot open.
		if c.AccessModeConnect != "disabled" || c.AccessModeExec != "disabled" ||
			c.AccessModeRunbooks != "disabled" || c.AccessSchema != "disabled" {
			t.Errorf("%s: want every access mode disabled, got connect=%s exec=%s runbooks=%s schema=%s",
				c.Name, c.AccessModeConnect, c.AccessModeExec, c.AccessModeRunbooks, c.AccessSchema)
		}
	}
	if got[0].SidecarListener.String != "appdb" || got[1].SidecarListener.String != "api.v2_beta" {
		t.Errorf("want the listener name kept verbatim, got %q, %q",
			got[0].SidecarListener.String, got[1].SidecarListener.String)
	}
}

func TestProjectListenersRefusesAProtocolWithNoConnectionType(t *testing.T) {
	for _, protocol := range []string{"redis", ""} {
		got, err := ProjectListeners("org-1", sidecarWith("pay", daemon.ListenerConfig{Name: "appdb", Protocol: protocol}))
		if err == nil || !strings.Contains(err.Error(), `no connection type for protocol "`+protocol+`"`) {
			t.Errorf("protocol %q: want the no-connection-type error, got %v", protocol, err)
		}
		if got != nil {
			t.Errorf("protocol %q: want no partial result, got %d connections", protocol, len(got))
		}
	}

	// One bad listener fails the whole projection: a sidecar is mirrored
	// whole or not at all.
	_, err := ProjectListeners("org-1", sidecarWith("pay",
		daemon.ListenerConfig{Name: "ok", Protocol: "postgres"},
		daemon.ListenerConfig{Name: "bad", Protocol: "redis"},
	))
	if err == nil {
		t.Error("want the projection to fail on the second listener")
	}
}

// The daemon accepts listener names the connection name rule refuses. A
// sidecar config that works today must keep working, so those listeners take
// the fallback name instead of refusing the write.
func TestProjectListenersFallsBackForANameTheRuleRefuses(t *testing.T) {
	cases := []struct {
		name, sidecar, listener string
	}{
		{"listener name with a space", "pay", "app db"},
		{"sidecar name with a slash", "pay/ments", "appdb"},
		// "pay-" plus 125 is 129, one over resources.name.
		{"name over the resource width", "pay", strings.Repeat("a", 125)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := sidecarWith(tc.sidecar, daemon.ListenerConfig{Name: tc.listener, Protocol: "postgres"})
			got, err := ProjectListeners("org-1", sc)
			if err != nil {
				t.Fatalf("a name the rule refuses must not refuse the sidecar: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("want 1 connection, got %d", len(got))
			}
			want := models.SidecarMirrorFallbackName(tc.sidecar+"-"+tc.listener, sc.ID, tc.listener)
			if got[0].Name != want {
				t.Errorf("want %q, got %q", want, got[0].Name)
			}
			if err := apivalidation.ValidateResourceName(got[0].Name); err != nil || len(got[0].Name) > models.MaxSidecarMirrorNameLength {
				t.Errorf("the fallback name %q breaks the name rule: %v", got[0].Name, err)
			}
			if got[0].SidecarListener.String != tc.listener {
				t.Errorf("the listener name must be kept verbatim, got %q", got[0].SidecarListener.String)
			}
		})
	}

	// "pay-" plus 124 is exactly 128: the preferred name, no fallback.
	got, err := ProjectListeners("org-1", sidecarWith("pay", daemon.ListenerConfig{Name: strings.Repeat("a", 124), Protocol: "postgres"}))
	if err != nil || got[0].Name != "pay-"+strings.Repeat("a", 124) {
		t.Errorf("a 128-character name must be kept: %v", err)
	}
}

func TestSidecarMirrorFallbackName(t *testing.T) {
	a := models.SidecarMirrorFallbackName("pay-cache", "sidecar-a", "cache")
	if a != models.SidecarMirrorFallbackName("pay-cache", "sidecar-a", "cache") {
		t.Error("the fallback name must be stable")
	}
	if a == models.SidecarMirrorFallbackName("pay-cache", "sidecar-b", "cache") {
		t.Error("two sidecars must not share a fallback name")
	}
	if a != models.SidecarMirrorFallbackName(a, "sidecar-a", "cache") {
		t.Error("the fallback of a fallback name must be that name")
	}
	for _, preferred := range []string{"pay-cache", "pay- -//x", "--", strings.Repeat("b", 300)} {
		got := models.SidecarMirrorFallbackName(preferred, "sidecar-a", "x")
		if err := apivalidation.ValidateResourceName(got); err != nil || len(got) > models.MaxSidecarMirrorNameLength {
			t.Errorf("%q falls back to %q, which breaks the name rule: %v", preferred, got, err)
		}
	}
}

// Every feature reads a listener through its mirror, so a listener that cannot
// have one is refused, never skipped: a skipped one would sit outside those
// features with no error. The message names the listener.
func TestProjectListenersRefusesListenersNoMirrorCanAddress(t *testing.T) {
	appdb := daemon.ListenerConfig{Name: "appdb", Protocol: "postgres"}
	cases := []struct {
		name      string
		listeners []daemon.ListenerConfig
		want      string
	}{
		{"no name", []daemon.ListenerConfig{appdb, {Protocol: "postgres"}}, "listeners[1]: no name"},
		{"a repeated name", []daemon.ListenerConfig{appdb, {Name: "appdb", Protocol: "mysql"}}, `listeners[1]: the name "appdb" repeats listeners[0]`},
		// One over connections.sidecar_listener: the insert would fail.
		{"a name the column cannot hold", []daemon.ListenerConfig{{Name: strings.Repeat("a", models.MaxSidecarListenerNameLength+1), Protocol: "postgres"}},
			"listeners[0]: the name is 256 characters, over 255"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProjectListeners("org-1", sidecarWith("pay", tc.listeners...))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error containing %q, got %v", tc.want, err)
			}
			if got != nil {
				t.Errorf("want no partial result, got %d mirrors", len(got))
			}
		})
	}

	// The width counts characters, as VARCHAR does: a multibyte name that
	// fits keeps its mirror.
	wide := strings.Repeat("é", models.MaxSidecarListenerNameLength)
	got, err := ProjectListeners("org-1", sidecarWith("pay", daemon.ListenerConfig{Name: wide, Protocol: "postgres"}))
	if err != nil || len(got) != 1 || got[0].SidecarListener.String != wide {
		t.Errorf("a %d-character name fits the column and must be mirrored: %v, %d mirrors", models.MaxSidecarListenerNameLength, err, len(got))
	}
}

func TestProjectListenersWithoutListeners(t *testing.T) {
	for _, sc := range []*models.Sidecar{
		sidecarWith("pay"),
		{ID: "id", Name: "pay"},
	} {
		got, err := ProjectListeners("org-1", sc)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("want no connections, got %d", len(got))
		}
	}
}

func TestProjectListenersIsDeterministic(t *testing.T) {
	sc := sidecarWith("pay",
		daemon.ListenerConfig{Name: "b", Protocol: "mysql"},
		daemon.ListenerConfig{Name: "a", Protocol: "postgres"},
		daemon.ListenerConfig{Name: "c", Protocol: "ssh"},
	)
	first, err := ProjectListeners("org-1", sc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProjectListeners("org-1", sc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("same input, different output:\n%+v\n%+v", first, second)
	}
	for i, want := range []string{"b", "a", "c"} {
		if first[i].SidecarListener.String != want {
			t.Errorf("position %d: want listener %q (config order, not sorted), got %q", i, want, first[i].SidecarListener.String)
		}
	}
}
