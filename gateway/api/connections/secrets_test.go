package apiconnections

import (
	"encoding/base64"
	"testing"
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

	t.Run("inline values are blanked, references are kept, keys are preserved", func(t *testing.T) {
		in := map[string]string{
			"envvar:HOST":     b64("db.internal"),
			"envvar:PASS":     b64("hunter2"),
			"envvar:AWS_PASS": b64("_aws:prod/db:password"),
			"envvar:IAM_USER": b64("_aws_iam_rds:user@cluster"),
		}
		out := stripInlineSecrets(in)

		if len(out) != len(in) {
			t.Fatalf("expected same key count, got %d vs %d", len(out), len(in))
		}
		if out["envvar:HOST"] != "" {
			t.Errorf("inline HOST should be blanked, got %q", out["envvar:HOST"])
		}
		if out["envvar:PASS"] != "" {
			t.Errorf("inline PASS should be blanked, got %q", out["envvar:PASS"])
		}
		if out["envvar:AWS_PASS"] != in["envvar:AWS_PASS"] {
			t.Errorf("AWS reference should be preserved")
		}
		if out["envvar:IAM_USER"] != in["envvar:IAM_USER"] {
			t.Errorf("IAM RDS reference should be preserved")
		}
	})

	t.Run("boolean values round-trip so toggle UIs reflect actual state", func(t *testing.T) {
		in := map[string]string{
			"envvar:INSECURE":   b64("true"),
			"envvar:DEBUG":      b64("false"),
			"envvar:PASS":       b64("hunter2"),
			"envvar:TRUE_LOOKS": b64("true123"), // not exactly "true"
		}
		out := stripInlineSecrets(in)
		if out["envvar:INSECURE"] != in["envvar:INSECURE"] {
			t.Errorf("base64(\"true\") should be preserved, got %q", out["envvar:INSECURE"])
		}
		if out["envvar:DEBUG"] != in["envvar:DEBUG"] {
			t.Errorf("base64(\"false\") should be preserved, got %q", out["envvar:DEBUG"])
		}
		if out["envvar:PASS"] != "" {
			t.Errorf("non-boolean inline value should still be stripped")
		}
		if out["envvar:TRUE_LOOKS"] != "" {
			t.Errorf("only exact 'true'/'false' decoded values are exempt; got %q", out["envvar:TRUE_LOOKS"])
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

func TestHasAnySecretReference(t *testing.T) {
	cases := []struct {
		name     string
		envs     map[string]string
		expected bool
	}{
		{"nil map", nil, false},
		{"empty map", map[string]string{}, false},
		{"only inline values", map[string]string{"envvar:HOST": b64("db.internal"), "envvar:PASS": b64("hunter2")}, false},
		{"only boolean values", map[string]string{"envvar:INSECURE": b64("true")}, false},
		{"single aws reference", map[string]string{"envvar:PASS": b64("_aws:prod/db:password")}, true},
		{"single aws_iam_rds reference", map[string]string{"envvar:USER": b64("_aws_iam_rds:appuser")}, true},
		{"single vaultkv1 reference", map[string]string{"envvar:USER": b64("_vaultkv1:secret/db:user")}, true},
		{"mixed inline + reference", map[string]string{"envvar:HOST": b64("db.internal"), "envvar:USER": b64("_aws:prod/db:user")}, true},
		{"non-base64 value ignored", map[string]string{"envvar:HOST": "not-base64!!"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasAnySecretReference(tc.envs); got != tc.expected {
				t.Fatalf("hasAnySecretReference = %v, want %v", got, tc.expected)
			}
		})
	}
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

func TestMergeSecrets(t *testing.T) {
	t.Run("nil patch is no-op", func(t *testing.T) {
		existing := map[string]string{"envvar:PASS": b64("hunter2")}
		out, changed := mergeSecrets(existing, nil)
		if changed {
			t.Errorf("nil patch should not mark as changed")
		}
		if !envsEqual(out, existing) {
			t.Errorf("nil patch should return identical map")
		}
	})

	t.Run("non-empty value replaces existing", func(t *testing.T) {
		existing := map[string]string{"envvar:PASS": b64("old")}
		patch := map[string]string{"envvar:PASS": b64("new")}
		out, changed := mergeSecrets(existing, patch)
		if !changed {
			t.Errorf("expected changed=true")
		}
		if out["envvar:PASS"] != b64("new") {
			t.Errorf("expected new value, got %q", out["envvar:PASS"])
		}
	})

	t.Run("non-empty value adds new key", func(t *testing.T) {
		existing := map[string]string{"envvar:PASS": b64("hunter2")}
		patch := map[string]string{"envvar:NEW": b64("value")}
		out, changed := mergeSecrets(existing, patch)
		if !changed {
			t.Errorf("expected changed=true")
		}
		if out["envvar:PASS"] != b64("hunter2") {
			t.Errorf("existing key should be preserved")
		}
		if out["envvar:NEW"] != b64("value") {
			t.Errorf("expected new key added")
		}
	})

	t.Run("empty value deletes existing key", func(t *testing.T) {
		existing := map[string]string{
			"envvar:PASS":   b64("hunter2"),
			"envvar:CUSTOM": b64("value"),
		}
		patch := map[string]string{"envvar:CUSTOM": ""}
		out, changed := mergeSecrets(existing, patch)
		if !changed {
			t.Errorf("expected changed=true")
		}
		if _, exists := out["envvar:CUSTOM"]; exists {
			t.Errorf("key should be deleted")
		}
		if out["envvar:PASS"] != b64("hunter2") {
			t.Errorf("untouched key should be preserved")
		}
	})

	t.Run("empty value for missing key is no-op", func(t *testing.T) {
		existing := map[string]string{"envvar:PASS": b64("hunter2")}
		patch := map[string]string{"envvar:GHOST": ""}
		out, changed := mergeSecrets(existing, patch)
		if changed {
			t.Errorf("deleting a non-existent key should not mark as changed")
		}
		if !envsEqual(out, existing) {
			t.Errorf("map should be unchanged")
		}
	})

	t.Run("absent keys are preserved", func(t *testing.T) {
		existing := map[string]string{
			"envvar:HOST": b64("db.internal"),
			"envvar:USER": b64("admin"),
			"envvar:PASS": b64("hunter2"),
		}
		patch := map[string]string{"envvar:PASS": b64("new-password")}
		out, _ := mergeSecrets(existing, patch)
		if out["envvar:HOST"] != b64("db.internal") {
			t.Errorf("HOST should be preserved")
		}
		if out["envvar:USER"] != b64("admin") {
			t.Errorf("USER should be preserved")
		}
		if out["envvar:PASS"] != b64("new-password") {
			t.Errorf("PASS should be replaced")
		}
	})

	t.Run("identical replacement does not mark as changed", func(t *testing.T) {
		existing := map[string]string{"envvar:PASS": b64("hunter2")}
		patch := map[string]string{"envvar:PASS": b64("hunter2")}
		_, changed := mergeSecrets(existing, patch)
		if changed {
			t.Errorf("identical value should not mark as changed")
		}
	})

	t.Run("does not mutate inputs", func(t *testing.T) {
		existing := map[string]string{"envvar:PASS": b64("hunter2")}
		patch := map[string]string{"envvar:PASS": b64("new")}
		out, _ := mergeSecrets(existing, patch)
		out["envvar:OTHER"] = "polluted"
		if _, exists := existing["envvar:OTHER"]; exists {
			t.Errorf("existing should not have been mutated via output reference")
		}
	})
}
