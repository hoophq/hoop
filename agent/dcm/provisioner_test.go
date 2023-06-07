package dcm

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseConfig(t *testing.T) {
	sessionUserPwd := "session-user-pwd"
	for _, tt := range []struct {
		msg      string
		data     map[string]any
		wantErr  error
		wantCred *Credentials
	}{
		{
			msg: "it should parse it and match credentials",
			data: map[string]any{
				"name":             "pgread",
				"engine":           "postgres",
				"datasource":       "postgres://bob:123@127.0.0.1/postgres",
				"instances":        []string{"postgres.public"},
				"expiration":       "1s",
				"grant-privileges": []string{"SELECT"},
				"checksum":         "hash",
			},
			wantCred: &Credentials{
				policyName:            "pgread",
				policyEngine:          postgresEngineType,
				policyExpiration:      time.Second * 1,
				policyInstances:       []string{"postgres.public"},
				policyGrantPrivileges: []string{"SELECT"},
				policyMainDatasource:  "postgres://bob:123@127.0.0.1:5432/postgres?connect_timeout=5",
				policyChecksum:        "hash",

				Host:     "127.0.0.1",
				Port:     "5432",
				Username: newSessionUserName("postgres.public:SELECT"),
				Password: sessionUserPwd,

				dbSuperUser:     "bob",
				dbSuperUserPwd:  "123",
				dbDriverOptions: "connect_timeout=5",
			},
		},
		{
			msg: "it should return error unknow engine error",
			data: map[string]any{
				"name":       "pgread",
				"datasource": "postgres://bob:123@127.0.0.1/postgres",
				"expiration": "1s",
				"engine":     "engine-foo",
			},
			wantErr: errUnknownEngine,
		},
		{
			msg: "it should return error when policy name is empty",
			data: map[string]any{
				"datasource": "postgres://bob:123@127.0.0.1/postgres",
				"expiration": "1s",
			},
			wantErr: errMissingPolicyName,
		},
		{
			msg: "it should return error when datasource is missing",
			data: map[string]any{
				"name":       "pgread",
				"datasource": "",
				"expiration": "1s",
				"engine":     postgresEngineType,
			},
			wantErr: fmt.Errorf("failed parsing datasource: invalid database scheme"),
		},
		{
			msg: "it should return error when missing instances config",
			data: map[string]any{
				"name":       "pgread",
				"datasource": "postgres://bob:123@127.0.0.1/postgres",
				"expiration": "1s",
				"engine":     postgresEngineType,
				"instances":  []string{},
			},
			wantErr: errMissingInstancesConfig,
		},
		{
			msg: "it should return error when missing grant privileges",
			data: map[string]any{
				"name":             "pgread",
				"datasource":       "postgres://bob:123@127.0.0.1/postgres",
				"expiration":       "1s",
				"engine":           postgresEngineType,
				"instances":        []string{"postgres.public"},
				"grant-privileges": []string{},
			},
			wantErr: errMissingGrantPrivileges,
		},
		{
			msg: "it should return error when missing checksum",
			data: map[string]any{
				"name":             "pgread",
				"datasource":       "postgres://bob:123@127.0.0.1/postgres",
				"expiration":       "1s",
				"engine":           postgresEngineType,
				"instances":        []string{"postgres.public"},
				"grant-privileges": []string{"SELECT"},
				"checksum":         "",
			},
			wantErr: errMissingChecksum,
		},
		{
			msg: "it should return error when policy expiration is empty",
			data: map[string]any{
				"name":             "pgread",
				"datasource":       "postgres://bob:123@127.0.0.1/postgres",
				"expiration":       "0s",
				"engine":           postgresEngineType,
				"instances":        []string{"postgres.public"},
				"grant-privileges": []string{"SELECT"},
				"checksum":         "checksum",
			},
			wantErr: errMissingExpiration,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got, err := parseConfig(tt.data, sessionUserPwd)
			assert.Equal(t, err, tt.wantErr, tt.msg)
			if tt.wantCred != nil {
				assert.Equal(t, got, tt.wantCred, tt.msg)
			}
		})
	}
}

func TestSessionUserSuffix(t *testing.T) {
	newPolicyConfigFn := func(instances, privileges []string) map[string]any {
		return map[string]any{
			"name":             "pgread",
			"engine":           "postgres",
			"datasource":       "postgres://bob:123@127.0.0.1/postgres",
			"instances":        instances,
			"grant-privileges": privileges,
			"expiration":       "1s",
			"checksum":         "hash",
		}
	}

	for _, tt := range []struct {
		msg      string
		data     map[string]any
		wantUser string
	}{
		{
			msg: "it should match session user crc32 suffix",
			data: newPolicyConfigFn(
				[]string{"a123.public", "b123.public"},
				[]string{"SELECT", "UPDATE"}),
			wantUser: newSessionUserName("a123.public,b123.public:SELECT,UPDATE"),
		},
		{
			msg: "it should match session user crc32 suffix with unsorted inputs",
			data: newPolicyConfigFn(
				[]string{"b123.public", "a123.public"},
				[]string{"UPDATE", "SELECT"}),
			wantUser: newSessionUserName("a123.public,b123.public:SELECT,UPDATE"),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got, err := parseConfig(tt.data, "")
			assert.Nil(t, err)
			assert.Equal(t, got.Username, tt.wantUser)
		})
	}
}

func TestRandomPasswordGenerator(t *testing.T) {
	for _, tt := range []struct {
		msg           string
		wantMinLength int
	}{
		{
			msg:           "it must generate a random password with success",
			wantMinLength: 25,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			passwd, gotErr := NewRandomPassword()
			assert.Nil(t, gotErr, tt.msg)
			assert.GreaterOrEqual(t, len(passwd), tt.wantMinLength, tt.msg)
		})
	}
}
