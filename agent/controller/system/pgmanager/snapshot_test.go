package pgmanager

import "testing"

// ---------------------------------------------------------------------------
// inDesired
// ---------------------------------------------------------------------------

func TestInDesired(t *testing.T) {
	schemas := map[string][]string{
		"mydb":    {"public", "analytics"},
		"otherdb": {"reporting"},
	}

	tests := []struct {
		db, schema string
		want       bool
	}{
		{"mydb", "public", true},
		{"mydb", "analytics", true},
		{"otherdb", "reporting", true},
		{"mydb", "missing", false},
		{"otherdb", "public", false},
		{"unknowndb", "public", false},
		{"", "", false},
	}

	for _, tt := range tests {
		got := inDesired(tt.db, tt.schema, schemas)
		if got != tt.want {
			t.Errorf("inDesired(%q, %q) = %v, want %v", tt.db, tt.schema, got, tt.want)
		}
	}
}

func TestInDesired_EmptyMap(t *testing.T) {
	if inDesired("mydb", "public", map[string][]string{}) {
		t.Error("expected false for empty schemas map")
	}
}

func TestInDesired_NilMap(t *testing.T) {
	if inDesired("mydb", "public", nil) {
		t.Error("expected false for nil schemas map")
	}
}
