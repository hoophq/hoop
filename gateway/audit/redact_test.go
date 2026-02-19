package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactNilMap(t *testing.T) {
	assert.Nil(t, Redact(nil))
}

func TestRedactEmptyMap(t *testing.T) {
	result := Redact(map[string]any{})
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestRedactNoSensitiveKeys(t *testing.T) {
	input := map[string]any{
		"name":        "my-conn",
		"type":        "postgres",
		"description": "production db",
	}

	result := Redact(input)

	assert.Equal(t, "my-conn", result["name"])
	assert.Equal(t, "postgres", result["type"])
	assert.Equal(t, "production db", result["description"])
}

func TestRedactSensitiveKeys(t *testing.T) {
	for _, key := range []string{
		"password", "hashed_password", "client_secret",
		"secret", "secrets", "api_key", "token", "key",
		"env", "envs", "rollout_api_key", "hosts_key",
	} {
		t.Run(key, func(t *testing.T) {
			input := map[string]any{key: "super-secret-value"}
			result := Redact(input)
			assert.Equal(t, "[REDACTED]", result[key])
		})
	}
}

func TestRedactMixedKeys(t *testing.T) {
	input := map[string]any{
		"name":     "my-service",
		"password": "s3cret",
		"token":    "tok-abc",
		"type":     "postgres",
	}

	result := Redact(input)

	assert.Equal(t, "my-service", result["name"])
	assert.Equal(t, "[REDACTED]", result["password"])
	assert.Equal(t, "[REDACTED]", result["token"])
	assert.Equal(t, "postgres", result["type"])
}

func TestRedactNestedMap(t *testing.T) {
	input := map[string]any{
		"config": map[string]any{
			"host":     "localhost",
			"password": "nested-secret",
		},
	}

	result := Redact(input)

	nested, ok := result["config"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "localhost", nested["host"])
	assert.Equal(t, "[REDACTED]", nested["password"])
}

func TestRedactDeeplyNestedMap(t *testing.T) {
	input := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"api_key": "deep-secret",
				"name":    "safe",
			},
		},
	}

	result := Redact(input)

	l1 := result["level1"].(map[string]any)
	l2 := l1["level2"].(map[string]any)
	assert.Equal(t, "[REDACTED]", l2["api_key"])
	assert.Equal(t, "safe", l2["name"])
}

func TestRedactSliceOfMaps(t *testing.T) {
	input := map[string]any{
		"connections": []map[string]any{
			{"name": "db1", "password": "p1"},
			{"name": "db2", "password": "p2", "token": "tok"},
		},
	}

	result := Redact(input)

	items, ok := result["connections"].([]map[string]any)
	assert.True(t, ok)
	assert.Len(t, items, 2)
	assert.Equal(t, "db1", items[0]["name"])
	assert.Equal(t, "[REDACTED]", items[0]["password"])
	assert.Equal(t, "db2", items[1]["name"])
	assert.Equal(t, "[REDACTED]", items[1]["password"])
	assert.Equal(t, "[REDACTED]", items[1]["token"])
}

func TestRedactDoesNotMutateOriginal(t *testing.T) {
	input := map[string]any{
		"password": "original",
		"name":     "test",
	}

	_ = Redact(input)

	assert.Equal(t, "original", input["password"])
	assert.Equal(t, "test", input["name"])
}

func TestRedactStructValue(t *testing.T) {
	type sshConfig struct {
		ListenAddress string `json:"listen_address"`
		HostsKey      string `json:"hosts_key"`
	}

	input := map[string]any{
		"ssh_server_config": sshConfig{
			ListenAddress: "0.0.0.0:22",
			HostsKey:      "private-key-material",
		},
	}

	result := Redact(input)

	nested, ok := result["ssh_server_config"].(map[string]any)
	assert.True(t, ok, "struct should be converted to map via JSON round-trip")
	assert.Equal(t, "0.0.0.0:22", nested["listen_address"])
	assert.Equal(t, "[REDACTED]", nested["hosts_key"])
}

func TestRedactStructPointerValue(t *testing.T) {
	type rdpConfig struct {
		ListenAddress string `json:"listen_address"`
		Password      string `json:"password"`
	}

	input := map[string]any{
		"rdp_config": &rdpConfig{
			ListenAddress: "0.0.0.0:3389",
			Password:      "s3cret",
		},
	}

	result := Redact(input)

	nested, ok := result["rdp_config"].(map[string]any)
	assert.True(t, ok, "struct pointer should be converted to map via JSON round-trip")
	assert.Equal(t, "0.0.0.0:3389", nested["listen_address"])
	assert.Equal(t, "[REDACTED]", nested["password"])
}

func TestRedactNonStringValues(t *testing.T) {
	input := map[string]any{
		"count":   42,
		"enabled": true,
		"tags":    []string{"a", "b"},
		"secret":  12345,
	}

	result := Redact(input)

	assert.Equal(t, 42, result["count"])
	assert.Equal(t, true, result["enabled"])
	assert.Equal(t, []string{"a", "b"}, result["tags"])
	assert.Equal(t, "[REDACTED]", result["secret"])
}
