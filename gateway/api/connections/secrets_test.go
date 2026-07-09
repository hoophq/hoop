package apiconnections

import (
	"encoding/base64"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestIsSecretReference(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		expected bool
	}{
		{"empty string", "", false},
		{"plain password", b64("hunter2"), false},
		{"plain host", b64("db.internal:5432"), false},
		{"aws secrets manager", b64("_aws:my-secret:password"), true},
		{"envjson reference", b64("_envjson:MYENV:KEY"), true},
		{"vault kv1", b64("_vaultkv1:secret/path:key"), true},
		{"vault kv2", b64("_vaultkv2:secret/path:key"), true},
		{"aws iam rds", b64("_aws_iam_rds:user@host:port"), true},
		{"not base64", "not-base64!!!", false},
		{"empty base64", b64(""), false},
		{"prefix-like but not a secret reference", b64("_other:foo:bar"), false},
		{"starts with underscore but no colon", b64("_just-text"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsSecretReference(tc.value)
			if got != tc.expected {
				t.Fatalf("IsSecretReference(%q) = %v, want %v", tc.value, got, tc.expected)
			}
		})
	}
}

func TestStripInlineSecrets(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		if got := stripInlineSecrets(nil); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})

	t.Run("inline values are masked to nil, references are kept, keys are preserved", func(t *testing.T) {
		in := map[string]string{
			"envvar:HOST":     b64("db.internal"),
			"envvar:PASS":     b64("hunter2"),
			"envvar:AWS_PASS": b64("_aws:prod/db:password"),
			"envvar:IAM_PASS": b64("_aws_iam_rds:authtoken"),
		}
		out := stripInlineSecrets(in)

		if len(out) != len(in) {
			t.Fatalf("expected same key count, got %d vs %d", len(out), len(in))
		}
		if out["envvar:HOST"] != nil {
			t.Errorf("inline HOST should be masked, got %v", out["envvar:HOST"])
		}
		if out["envvar:PASS"] != nil {
			t.Errorf("inline PASS should be masked, got %v", out["envvar:PASS"])
		}
		if out["envvar:AWS_PASS"] != in["envvar:AWS_PASS"] {
			t.Errorf("AWS reference should be preserved")
		}
		if out["envvar:IAM_PASS"] != in["envvar:IAM_PASS"] {
			t.Errorf("IAM RDS mode marker should be preserved")
		}
	})

	t.Run("boolean values are masked like any other inline secret", func(t *testing.T) {
		// Deliberate: a toggle value arrives in the same base64 secret
		// map as every other envvar, so it is masked uniformly and the
		// UI gives it a write-only Replace-to-reveal switch.
		in := map[string]string{
			"envvar:INSECURE": b64("true"),
			"envvar:DEBUG":    b64("false"),
		}
		out := stripInlineSecrets(in)
		if out["envvar:INSECURE"] != nil {
			t.Errorf("base64(\"true\") should be masked, got %v", out["envvar:INSECURE"])
		}
		if out["envvar:DEBUG"] != nil {
			t.Errorf("base64(\"false\") should be masked, got %v", out["envvar:DEBUG"])
		}
	})

	t.Run("does not mutate input", func(t *testing.T) {
		in := map[string]string{"envvar:PASS": b64("hunter2")}
		original := in["envvar:PASS"]
		_ = stripInlineSecrets(in)
		if in["envvar:PASS"] != original {
			t.Fatalf("input was mutated")
		}
	})
}

func TestToOpenApiDefaultDatabaseMasking(t *testing.T) {
	conn := &models.Connection{
		Envs: map[string]string{
			"envvar:DB":   b64("customers"),
			"envvar:PASS": b64("hunter2"),
		},
	}

	t.Run("hide_role_info off returns the decoded value", func(t *testing.T) {
		out := ToOpenApi(conn, false)
		if out.DefaultDatabase != "customers" {
			t.Fatalf("DefaultDatabase = %q, want %q", out.DefaultDatabase, "customers")
		}
	})

	t.Run("hide_role_info on masks the value", func(t *testing.T) {
		out := ToOpenApi(conn, true)
		if out.DefaultDatabase != "" {
			t.Fatalf("DefaultDatabase = %q, want empty: envvar:DB comes from the secret env map and must not be readable back", out.DefaultDatabase)
		}
		if out.Secrets["envvar:DB"] != nil {
			t.Fatalf("Secrets[envvar:DB] = %v, want nil", out.Secrets["envvar:DB"])
		}
	})
}

func TestEnvsEqual(t *testing.T) {
	cases := []struct {
		name     string
		a, b     map[string]string
		expected bool
	}{
		{"both nil", nil, nil, true},
		{"nil and empty", nil, map[string]string{}, true},
		{"same content", map[string]string{"k": "v"}, map[string]string{"k": "v"}, true},
		{"different value", map[string]string{"k": "v"}, map[string]string{"k": "v2"}, false},
		{"different keys", map[string]string{"k": "v"}, map[string]string{"k2": "v"}, false},
		{"extra key on right", map[string]string{"k": "v"}, map[string]string{"k": "v", "k2": "v"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := envsEqual(tc.a, tc.b); got != tc.expected {
				t.Fatalf("envsEqual = %v, want %v", got, tc.expected)
			}
		})
	}
}
