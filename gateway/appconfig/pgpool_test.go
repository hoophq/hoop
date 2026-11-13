package appconfig

import "testing"

// POSTGRES_MAX_OPEN_CONNS: unset takes the default; a value that is not a
// positive integer stops startup, since 0 would mean an unbounded pool.
func TestLoadPgMaxOpenConns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     string
		wantErr bool
		want    int
	}{
		{"unset", "", false, defaultPgMaxOpenConns},
		{"valid", "20", false, 20},
		{"zero", "0", true, 0},
		{"negative", "-1", true, 0},
		{"not a number", "many", true, 0},
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
				t.Fatalf("Load failed: %v", err)
			}
			if got := Get().PgMaxOpenConns(); got != tc.want {
				t.Fatalf("PgMaxOpenConns() = %d, want %d", got, tc.want)
			}
		})
	}
}
