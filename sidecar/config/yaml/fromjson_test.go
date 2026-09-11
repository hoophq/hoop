package yaml

import (
	"encoding/json"
	"strings"
	"testing"
)

// FromJSON must preserve the document's key order: -migrate emits a file a
// person maintains, and json.Marshal ordered the keys by struct
// declaration, which is the order the docs teach.
func TestFromJSONPreservesKeyOrder(t *testing.T) {
	in := `{"analyzer":{"provider":"vertex","model":"m"},"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1"}]}`
	out, err := FromJSON([]byte(in))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	s := string(out)
	for _, pair := range [][2]string{
		{"analyzer:", "listeners:"},
		{"provider:", "model:"},
		{"name:", "protocol:"},
		{"protocol:", "listen:"},
		{"listen:", "upstream:"},
	} {
		if strings.Index(s, pair[0]) > strings.Index(s, pair[1]) {
			t.Errorf("%s does not precede %s:\n%s", pair[0], pair[1], s)
		}
	}
}

// What FromJSON emits must read back through ToJSON as the same document:
// the migration's output is this package's own input.
func TestFromJSONRoundTripsThroughToJSON(t *testing.T) {
	in := `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",` +
		`"max_conns":10,"analyzer":{"trigger":{"operations":["delete"]},"high":"block",` +
		`"fail_open":false,"prompt":"line one\nline two"}}],` +
		`"guardrails":{"rules":[]},"opa":{"url":"http://opa:8181/v1/data/hoop"}}`

	y, err := FromJSON([]byte(in))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	back, err := ToJSON(y)
	if err != nil {
		t.Fatalf("ToJSON of the render: %v\n%s", err, y)
	}
	// Compare semantically: ToJSON's key order comes from a map walk.
	if got, want := canonical(t, back), canonical(t, []byte(in)); got != want {
		t.Errorf("round trip drifted:\n got %s\nwant %s\nyaml:\n%s", got, want, y)
	}
}

// Strings that look like other scalar types must come back as strings: a
// deny word "true" or a message "123" cannot change type on the way through.
func TestFromJSONQuotesAmbiguousStrings(t *testing.T) {
	in := `{"a":"true","b":"123","c":"null","d":"03:04"}`
	y, err := FromJSON([]byte(in))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	back, err := ToJSON(y)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if got, want := canonical(t, back), canonical(t, []byte(in)); got != want {
		t.Errorf("scalar types drifted:\n got %s\nwant %s\nyaml:\n%s", got, want, y)
	}
}

// canonical renders JSON bytes with sorted keys so two documents compare by
// content rather than by key order.
func canonical(t *testing.T, data []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}
