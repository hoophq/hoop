package admin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBatchFile(t *testing.T) {
	yamlContent := `
agent: default
type: database/postgres
overwrite: true
tags:
  env: production
  team: platform
access_modes:
  - connect
  - exec
connections:
  - name: users-db
    env:
      HOST: 10.0.1.10
      USER: admin
      PASS: secret
      PORT: "5432"
  - name: orders-db
    env:
      HOST: 10.0.1.11
      USER: admin
      PASS: secret
      PORT: "5432"
    tags:
      env: staging
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "connections.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte(yamlContent), 0644))

	batch, err := parseBatchFile(filePath)
	require.NoError(t, err)

	assert.Equal(t, "default", batch.Agent)
	assert.Equal(t, "database/postgres", batch.Type)
	assert.True(t, batch.Overwrite)
	assert.Equal(t, map[string]string{"env": "production", "team": "platform"}, batch.Tags)
	assert.Equal(t, []string{"connect", "exec"}, batch.AccessModes)
	assert.Len(t, batch.Connections, 2)

	assert.Equal(t, "users-db", batch.Connections[0].Name)
	assert.Equal(t, "10.0.1.10", batch.Connections[0].Env["HOST"])
	assert.Equal(t, "admin", batch.Connections[0].Env["USER"])

	assert.Equal(t, "orders-db", batch.Connections[1].Name)
	// Connection-level tags override top-level
	assert.Equal(t, "staging", batch.Connections[1].Tags["env"])
}

func TestParseBatchFileWithOverrides(t *testing.T) {
	yamlContent := `
agent: default
type: database/postgres
connections:
  - name: special-db
    agent: custom-agent
    type: database/mysql
    env:
      HOST: 10.0.1.50
      USER: root
      PASS: secret
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "connections.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte(yamlContent), 0644))

	batch, err := parseBatchFile(filePath)
	require.NoError(t, err)

	assert.Len(t, batch.Connections, 1)
	assert.Equal(t, "custom-agent", batch.Connections[0].Agent)
	assert.Equal(t, "database/mysql", batch.Connections[0].Type)
}

func TestParseBatchEnvVars(t *testing.T) {
	envMap := map[string]string{
		"HOST": "localhost",
		"PORT": "5432",
		"USER": "admin",
	}

	result, err := parseBatchEnvVars(envMap)
	require.NoError(t, err)

	assert.Len(t, result, 3)
	// Keys should be prefixed with "envvar:"
	assert.NotEmpty(t, result["envvar:HOST"])
	assert.NotEmpty(t, result["envvar:PORT"])
	assert.NotEmpty(t, result["envvar:USER"])
}

func TestParseBatchEnvVarsWithFileRef(t *testing.T) {
	tmpDir := t.TempDir()
	secretFile := filepath.Join(tmpDir, "secret.txt")
	require.NoError(t, os.WriteFile(secretFile, []byte("my-secret-password"), 0644))

	envMap := map[string]string{
		"HOST": "localhost",
		"PASS": "file://" + secretFile,
	}

	result, err := parseBatchEnvVars(envMap)
	require.NoError(t, err)

	assert.Len(t, result, 2)
	assert.NotEmpty(t, result["envvar:HOST"])
	assert.NotEmpty(t, result["envvar:PASS"])
}

func TestParseBatchFileEmpty(t *testing.T) {
	yamlContent := `
agent: default
type: database/postgres
connections: []
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "empty.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte(yamlContent), 0644))

	batch, err := parseBatchFile(filePath)
	require.NoError(t, err)

	assert.Empty(t, batch.Connections)
}

func TestValidateConnectionType(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		wantErr bool
	}{
		{
			name:    "valid postgres envs",
			envVars: map[string]string{"envvar:HOST": "val", "envvar:USER": "val", "envvar:PASS": "val"},
			wantErr: false,
		},
		{
			name:    "missing postgres envs",
			envVars: map[string]string{"envvar:HOST": "val"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConnectionType("postgres", tt.envVars, nil)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBatchAccessModeStatus(t *testing.T) {
	modes := []string{"connect", "exec"}

	assert.Equal(t, "enabled", batchAccessModeStatus(modes, "connect"))
	assert.Equal(t, "enabled", batchAccessModeStatus(modes, "exec"))
	assert.Equal(t, "disabled", batchAccessModeStatus(modes, "runbooks"))
}
