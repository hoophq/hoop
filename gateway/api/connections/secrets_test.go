package apiconnections

import (
	"database/sql"
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

func TestShouldRoundTripSecrets(t *testing.T) {
	mk := func(typ, sub string) *models.Connection {
		c := &models.Connection{Type: typ}
		if sub != "" {
			c.SubType = sql.NullString{String: sub, Valid: true}
		}
		return c
	}
	cases := []struct {
		name     string
		conn     *models.Connection
		expected bool
	}{
		{"nil", nil, false},

		// Catalog databases stay write-only — bespoke catalog renderer
		// drives the Set/Replace pattern.
		{"database/postgres stays write-only", mk("database", "postgres"), false},
		{"database/mysql stays write-only", mk("database", "mysql"), false},
		{"database/mssql stays write-only", mk("database", "mssql"), false},
		{"database/oracledb stays write-only", mk("database", "oracledb"), false},
		{"database/mongodb stays write-only", mk("database", "mongodb"), false},

		// application/ssh has a bespoke renderer (auth-method radio)
		// that needs host/user/PASS|AUTHORIZED_SERVER_KEYS visible.
		{"application/ssh round-trips", mk("application", "ssh"), true},

		// Catalog applications (git, github, tcp) ship full schemas and
		// render via the catalog renderer → write-only.
		{"application/git stays write-only", mk("application", "git"), false},
		{"application/github stays write-only", mk("application", "github"), false},
		{"application/tcp stays write-only", mk("application", "tcp"), false},
		{"unknown application subtype stays write-only", mk("application", "saas"), false},

		// Every httpproxy subtype has a bespoke renderer with HTTP
		// headers + insecure-SSL toggle on top of REMOTE_URL.
		{"httpproxy/claude-code round-trips", mk("httpproxy", "claude-code"), true},
		{"httpproxy/web-application round-trips", mk("httpproxy", "web-application"), true},
		{"httpproxy/grafana round-trips", mk("httpproxy", "grafana"), true},
		{"httpproxy/kibana round-trips", mk("httpproxy", "kibana"), true},
		{"httpproxy with no subtype round-trips", mk("httpproxy", ""), true},

		// Custom: round-trip only for the two bespoke shapes + the
		// free-form catch-all (no subtype). Catalog custom subtypes
		// (dynamodb, aws-*, kubernetes, redis, …) stay write-only.
		{"custom free-form round-trips", mk("custom", ""), true},
		{"custom/linux-vm round-trips", mk("custom", "linux-vm"), true},
		{"custom/kubernetes-token round-trips", mk("custom", "kubernetes-token"), true},
		{"custom/dynamodb stays write-only", mk("custom", "dynamodb"), false},
		{"custom/aws-cloudwatch stays write-only", mk("custom", "aws-cloudwatch"), false},
		{"custom/aws-cli stays write-only", mk("custom", "aws-cli"), false},
		{"custom/cassandra stays write-only", mk("custom", "cassandra"), false},
		{"custom/redis stays write-only", mk("custom", "redis"), false},
		{"custom/kubernetes stays write-only", mk("custom", "kubernetes"), false},
		{"custom/cloudwatch (legacy, no catalog entry) stays write-only",
			mk("custom", "cloudwatch"), false},

		// Unknown top-level type stays safe (write-only).
		{"unknown type stays write-only", mk("foo", "bar"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRoundTripSecrets(tc.conn)
			if got != tc.expected {
				t.Fatalf("shouldRoundTripSecrets(%+v) = %v, want %v", tc.conn, got, tc.expected)
			}
		})
	}
}

// Connections that carry any provider reference (Secrets Manager or
// AWS IAM Role mode) round-trip regardless of shape — the stored values
// are pointers/config, not raw secrets. This lets the UI render every
// field consistently instead of mixing locked Set/Replace cards with
// editable reference inputs in the same form.
func TestShouldRoundTripSecrets_ReferencePromotion(t *testing.T) {
	mk := func(typ, sub string, envs map[string]string) *models.Connection {
		c := &models.Connection{Type: typ, Envs: envs}
		if sub != "" {
			c.SubType = sql.NullString{String: sub, Valid: true}
		}
		return c
	}
	cases := []struct {
		name     string
		conn     *models.Connection
		expected bool
	}{
		{
			name: "database/postgres with AWS IAM USER promotes the whole connection",
			conn: mk("database", "postgres", map[string]string{
				"envvar:HOST": b64("db.internal"),
				"envvar:PORT": b64("5432"),
				"envvar:USER": b64("_aws_iam_rds:appuser"),
				"envvar:PASS": b64("_aws_iam_rds:authtoken"),
				"envvar:DB":   b64("appdb"),
			}),
			expected: true,
		},
		{
			name: "database/postgres with AWS Secrets Manager USER promotes",
			conn: mk("database", "postgres", map[string]string{
				"envvar:HOST": b64("db.internal"),
				"envvar:USER": b64("_aws:prod/db:user"),
				"envvar:PASS": b64("_aws:prod/db:password"),
			}),
			expected: true,
		},
		{
			name: "database/mysql with Vault KV v1 reference promotes",
			conn: mk("database", "mysql", map[string]string{
				"envvar:USER": b64("_vaultkv1:secret/db:user"),
			}),
			expected: true,
		},
		{
			name: "database/mssql with Vault KV v2 reference promotes",
			conn: mk("database", "mssql", map[string]string{
				"envvar:PASS": b64("_vaultkv2:secret/db:password"),
			}),
			expected: true,
		},
		{
			name: "application/git with reference promotes (was write-only by shape)",
			conn: mk("application", "git", map[string]string{
				"envvar:TOKEN": b64("_aws:ci/git:token"),
			}),
			expected: true,
		},
		{
			name: "custom/dynamodb with reference promotes (was write-only by shape)",
			conn: mk("custom", "dynamodb", map[string]string{
				"envvar:AWS_SECRET_ACCESS_KEY": b64("_aws:aws/keys:secret"),
			}),
			expected: true,
		},
		{
			name:     "database/postgres with only inline values stays write-only",
			conn:     mk("database", "postgres", map[string]string{"envvar:HOST": b64("db.internal"), "envvar:PASS": b64("hunter2")}),
			expected: false,
		},
		{
			name:     "database/postgres with only boolean values stays write-only",
			conn:     mk("database", "postgres", map[string]string{"envvar:INSECURE": b64("true")}),
			expected: false,
		},
		{
			name:     "database/postgres with empty envs stays write-only",
			conn:     mk("database", "postgres", map[string]string{}),
			expected: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRoundTripSecrets(tc.conn)
			if got != tc.expected {
				t.Fatalf("shouldRoundTripSecrets = %v, want %v", got, tc.expected)
			}
		})
	}
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
