package appconfig

import "testing"

// POSTGRES_MAX_OPEN_CONNS: unset caps the pool at the default, 0 removes the
// cap, and a value that is not a whole number stops startup rather than
// running without the cap the operator meant to set.
func TestLoadPgMaxOpenConns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     string
		wantErr bool
		want    int
	}{
		{"unset caps at the default", "", false, defaultPgMaxOpenConns},
		{"a number is the cap", "40", false, 40},
		{"zero removes the cap", "0", false, 0},
		{"negative refused", "-1", true, 0},
		{"not a number refused", "fifty", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POSTGRES_DB_URI", "postgres://u:p@localhost:5432/db")
			t.Setenv("POSTGRES_MAX_OPEN_CONNS", tc.env)
			runtimeConfig = Config{}

			err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected Load to fail, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := Get().PgMaxOpenConns(); got != tc.want {
				t.Fatalf("PgMaxOpenConns() = %d, want %d", got, tc.want)
			}
		})
	}
}
