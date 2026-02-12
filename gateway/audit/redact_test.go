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
		"env", "envs", "rollout_api_key",
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

func TestRedactDoesNotMutateOriginal(t *testing.T) {
	input := map[string]any{
		"password": "original",
		"name":     "test",
	}

	_ = Redact(input)

	assert.Equal(t, "original", input["password"])
	assert.Equal(t, "test", input["name"])
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
