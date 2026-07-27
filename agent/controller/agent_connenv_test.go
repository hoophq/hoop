package controller

import (
	"encoding/base64"
	"strings"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

// mysqlEnvVars builds the envvar map shape term.NewEnvVarStore expects
// (base64-encoded values under "envvar:" keys) for a MySQL connection.
func mysqlEnvVars(extra map[string]string) map[string]any {
	envs := map[string]any{
		"envvar:HOST": base64.StdEncoding.EncodeToString([]byte("127.0.0.1")),
		"envvar:USER": base64.StdEncoding.EncodeToString([]byte("root")),
		"envvar:PASS": base64.StdEncoding.EncodeToString([]byte("secret")),
	}
	for k, v := range extra {
		envs["envvar:"+k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return envs
}

func TestParseConnectionEnvVarsMySQLResultMetadata(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		unset   bool
		want    string
		wantErr bool
	}{
		{name: "unset defaults to on", unset: true, want: "on"},
		{name: "empty defaults to on", value: "", want: "on"},
		{name: "on", value: "on", want: "on"},
		{name: "true", value: "true", want: "on"},
		{name: "1", value: "1", want: "on"},
		{name: "off", value: "off", want: "off"},
		{name: "false", value: "false", want: "off"},
		{name: "0", value: "0", want: "off"},
		{name: "mixed case normalizes", value: "OFF", want: "off"},
		{name: "invalid value fails fast", value: "maybe", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extra := map[string]string{}
			if !tt.unset {
				extra["RESULT_METADATA"] = tt.value
			}
			env, err := parseConnectionEnvVars(mysqlEnvVars(extra), pb.ConnectionTypeMySQL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for RESULT_METADATA=%q, got nil", tt.value)
				}
				if !strings.Contains(err.Error(), "RESULT_METADATA") {
					t.Errorf("error %q should mention RESULT_METADATA", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if env.resultMetadata != tt.want {
				t.Errorf("resultMetadata = %q, want %q", env.resultMetadata, tt.want)
			}
		})
	}
}
