package apiserverconfig

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
)

func TestParsePostgresConfigState(t *testing.T) {
	tests := []struct {
		name          string
		currentState  *models.ServerMiscConfig
		newState      *models.ServerMiscConfig
		expectedConf  models.PostgresServerConfig
		expectedState instanceState
	}{
		{
			name:          "nil current state, nil new state - noop",
			currentState:  nil,
			newState:      nil,
			expectedConf:  models.PostgresServerConfig{},
			expectedState: instanceState(""),
		},
		{
			name: "nil current config, nil new config - noop",
			currentState: &models.ServerMiscConfig{
				PostgresServerConfig: nil,
			},
			newState: &models.ServerMiscConfig{
				PostgresServerConfig: nil,
			},
			expectedConf:  models.PostgresServerConfig{},
			expectedState: instanceState(""),
		},
		{
			name: "empty current config, new config with address - start",
			currentState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "",
				},
			},
			newState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "0.0.0.0:5432",
				},
			},
			expectedConf: models.PostgresServerConfig{
				ListenAddress: "0.0.0.0:5432",
			},
			expectedState: instanceStateStart,
		},
		{
			name:         "nil current state, new config with address - start",
			currentState: nil,
			newState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "0.0.0.0:5432",
				},
			},
			expectedConf: models.PostgresServerConfig{
				ListenAddress: "0.0.0.0:5432",
			},
			expectedState: instanceStateStart,
		},
		{
			name: "current config with address, empty new config - stop",
			currentState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "0.0.0.0:5432",
				},
			},
			newState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "",
				},
			},
			expectedConf:  models.PostgresServerConfig{},
			expectedState: instanceStateStop,
		},
		{
			name: "current config with address, nil new state - stop",
			currentState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "0.0.0.0:5432",
				},
			},
			newState:      nil,
			expectedConf:  models.PostgresServerConfig{},
			expectedState: instanceStateStop,
		},
		{
			name: "current config with address, different new address - restart (returns start)",
			currentState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "0.0.0.0:5432",
				},
			},
			newState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "0.0.0.0:5433",
				},
			},
			expectedConf: models.PostgresServerConfig{
				ListenAddress: "0.0.0.0:5433",
			},
			expectedState: instanceStateStart,
		},
		{
			name: "same address in current and new - noop",
			currentState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "0.0.0.0:5432",
				},
			},
			newState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "0.0.0.0:5432",
				},
			},
			expectedConf:  models.PostgresServerConfig{},
			expectedState: instanceState(""),
		},
		{
			name: "both configs empty - noop",
			currentState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "",
				},
			},
			newState: &models.ServerMiscConfig{
				PostgresServerConfig: &models.PostgresServerConfig{
					ListenAddress: "",
				},
			},
			expectedConf:  models.PostgresServerConfig{},
			expectedState: instanceState(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf, state := parsePostgresConfigState(tt.currentState, tt.newState)
			assert.Equal(t, tt.expectedConf, conf)
			assert.Equal(t, tt.expectedState, state)
		})
	}
}

func TestParseSSHConfigState(t *testing.T) {
	tests := []struct {
		name          string
		currentState  *models.ServerMiscConfig
		newState      *models.ServerMiscConfig
		expectedConf  models.SSHServerConfig
		expectedState instanceState
	}{
		{
			name:          "nil current state, nil new state - noop",
			currentState:  nil,
			newState:      nil,
			expectedConf:  models.SSHServerConfig{},
			expectedState: instanceState(""),
		},
		{
			name: "nil current config, nil new config - noop",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: nil,
			},
			newState: &models.ServerMiscConfig{
				SSHServerConfig: nil,
			},
			expectedConf:  models.SSHServerConfig{},
			expectedState: instanceState(""),
		},
		{
			name: "empty current config, new config with address - start",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "",
				},
			},
			newState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			expectedConf: models.SSHServerConfig{
				ListenAddress: "localhost:22",
				HostsKey:      "ssh-rsa AAAAB3...",
			},
			expectedState: instanceStateStart,
		},
		{
			name: "current config with address, empty new config - stop",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			newState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "",
				},
			},
			expectedConf:  models.SSHServerConfig{},
			expectedState: instanceStateStop,
		},
		{
			name: "current config with address, different new address - start",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			newState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "0.0.0.0:2222",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			expectedConf: models.SSHServerConfig{
				ListenAddress: "0.0.0.0:2222",
				HostsKey:      "ssh-rsa AAAAB3...",
			},
			expectedState: instanceStateStart,
		},
		{
			name: "same address, empty current hosts key, new hosts key - start",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "",
				},
			},
			newState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			expectedConf: models.SSHServerConfig{
				ListenAddress: "localhost:22",
				HostsKey:      "ssh-rsa AAAAB3...",
			},
			expectedState: instanceStateStart,
		},
		{
			name: "same address, current hosts key, empty new hosts key - stop",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			newState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "",
				},
			},
			expectedConf:  models.SSHServerConfig{},
			expectedState: instanceStateStop,
		},
		{
			name: "same address and hosts key - noop",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			newState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			expectedConf:  models.SSHServerConfig{},
			expectedState: instanceState(""),
		},
		{
			name: "both configs have address but empty hosts keys (it will generate new ones) - noop",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "",
				},
			},
			newState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "",
				},
			},
			expectedConf:  models.SSHServerConfig{},
			expectedState: instanceState(""),
		},
		{
			name: "different hosts key with same address - restart (hosts key change triggers restart)",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			newState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa DIFFERENT...",
				},
			},
			expectedConf: models.SSHServerConfig{
				ListenAddress: "localhost:22",
				HostsKey:      "ssh-rsa DIFFERENT...",
			},
			expectedState: instanceStateStart,
		},
		{
			name:         "nil current state, new config with address and hosts key - start",
			currentState: nil,
			newState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			expectedConf: models.SSHServerConfig{
				ListenAddress: "localhost:22",
				HostsKey:      "ssh-rsa AAAAB3...",
			},
			expectedState: instanceStateStart,
		},
		{
			name: "current config with address and hosts key, nil new state - stop",
			currentState: &models.ServerMiscConfig{
				SSHServerConfig: &models.SSHServerConfig{
					ListenAddress: "localhost:22",
					HostsKey:      "ssh-rsa AAAAB3...",
				},
			},
			newState:      nil,
			expectedConf:  models.SSHServerConfig{},
			expectedState: instanceStateStop,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf, state := parseSSHConfigState(tt.currentState, tt.newState)
			assert.Equal(t, tt.expectedConf, conf)
			assert.Equal(t, tt.expectedState, state)
		})
	}
}
