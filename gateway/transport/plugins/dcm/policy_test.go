package dcm

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/stretchr/testify/assert"
)

func TestParsePolicyConfig(t *testing.T) {
	newPluginFn := func(connName, connConfig, configEntry, configEntryVal, policyHcl string) *plugin.Plugin {
		encPolicyHcl := base64.StdEncoding.EncodeToString([]byte(policyHcl))
		encConfigEntryVal := base64.StdEncoding.EncodeToString([]byte(configEntryVal))
		return &plugin.Plugin{
			Connections: []plugin.Connection{{Name: connName, Config: []string{connConfig}}},
			Config: &plugin.PluginConfig{
				EnvVars: map[string]string{
					configEntry:         encConfigEntryVal,
					policyConfigKeyName: encPolicyHcl},
			}}
	}
	for _, tt := range []struct {
		msg        string
		pl         *plugin.Plugin
		wantPolicy *Policy
		wantErr    error
	}{
		{
			msg: "it should parse the policy without any error",
			pl: newPluginFn("pg", "pg-readonly", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres",
				`
			policy "pg-readonly" {
				engine               = "postgres"
				plugin_config_entry  = "pg-local"
				expiration           = "12h"
				instances            = ["dellstore", "dellstore.store", "testdb"]
				grant_privileges     = ["SELECT"]
			}`),
			wantPolicy: &Policy{
				Name:              "pg-readonly",
				Engine:            "postgres",
				PluginConfigEntry: "pg-local",
				Expiration:        "12h",
				Instances:         []string{"dellstore", "dellstore.store", "testdb"},
				GrantPrivileges:   []string{"SELECT"},
				datasource:        "postgres://foo:bar@127.0.0.1:5432/postgres",
			},
		},
		{
			msg:     "it should fail if policy config is empty",
			pl:      newPluginFn("pg", "pg-readonly", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres", ""),
			wantErr: errEmptyPolicyConfig,
		},
		{
			msg:     "it should fail when policy is missing required attributes",
			pl:      newPluginFn("pg", "pg-readonly", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres", `policy "pg-readonly" {}`),
			wantErr: fmt.Errorf(`policy.hcl:1,22-22: Missing required argument; The argument "engine" is required, but no definition was found., and 3 other diagnostic(s)`),
		},
		{
			msg: "it should fail when policy is repeated",
			pl: newPluginFn("pg", "pg-readonly", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres", `
			policy "pg-readonly" {
				engine               = "postgres"
				plugin_config_entry  = "pg-local"
				instances            = ["postgres.public"]
				grant_privileges     = ["SELECT"]
			}
			policy "pg-readonly" {
				engine               = "postgres"
				plugin_config_entry  = "pg-local"
				instances            = ["postgres.public"]
				grant_privileges     = ["SELECT"]
			}
			`),
			wantErr: fmt.Errorf(`policy name %v already exists`, "pg-readonly"),
		},
		{
			msg: "it should fail when connection config does match policy name",
			pl: newPluginFn("pg", "pg-readwrite", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres", `
			policy "pg-readonly" {
				engine               = "postgres"
				plugin_config_entry  = "pg-local"
				instances            = ["postgres.public"]
				grant_privileges     = ["SELECT"]
			}
			`),
			wantErr: fmt.Errorf(`policy %q not found for this connection`, "pg-readwrite"),
		},
		{
			msg: "it should fail if grant privileges are repeated",
			pl: newPluginFn("pg", "pg-readonly", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres", `
			policy "pg-readonly" {
				engine               = "postgres"
				plugin_config_entry  = "pg-local"
				instances            = ["postgres.public"]
				grant_privileges     = ["SELECT", "SELECT", "UPDATE", "UPDATE"]
			}
			`),
			wantErr: fmt.Errorf(`found repeated privilege(s) %v`, "[SELECT UPDATE]"),
		},
		{
			msg: "it should fail when configuring a non supported grant",
			pl: newPluginFn("pg", "pg-readonly", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres", `
			policy "pg-readonly" {
				engine               = "postgres"
				plugin_config_entry  = "pg-local"
				instances            = ["postgres.public"]
				grant_privileges     = ["UNKNOWN01", "UNKNOWN02"]
			}
			`),
			wantErr: fmt.Errorf(`privileges [UNKNOWN01 UNKNOWN02] are not allowed for this engine`),
		},
		{
			msg: "it should fail if it has repeated instances",
			pl: newPluginFn("pg", "pg-readonly", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres", `
			policy "pg-readonly" {
				engine               = "postgres"
				plugin_config_entry  = "pg-local"
				instances            = ["postgres.public", "postgres.public", "testdb", "testdb"]
				grant_privileges     = ["SELECT", "UPDATE"]
			}
			`),
			wantErr: fmt.Errorf(`found repeated instance(s) %v`, "[postgres.public testdb]"),
		},
		{
			msg: "it should fail if plugin config entry does not match",
			pl: newPluginFn("pg", "pg-readonly", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres", `
			policy "pg-readonly" {
				engine               = "postgres"
				plugin_config_entry  = "pg-homolog"
				instances            = ["postgres.public"]
				grant_privileges     = ["SELECT", "UPDATE"]
			}
			`),
			wantErr: fmt.Errorf(`failed retrieving database credentials, missing configuration entry for %v`, "pg-homolog"),
		},
		{
			msg: "it should fail if it pass max reached instances",
			pl: newPluginFn("pg", "pg-readonly", "pg-local", "postgres://foo:bar@127.0.0.1:5432/postgres", `
			policy "pg-readonly" {
				engine               = "postgres"
				plugin_config_entry  = "pg-local"
				instances            = ["01", "02", "03", "04", "05", "06", "07", "08", "09", "10"]
				grant_privileges     = ["SELECT", "UPDATE"]
			}
			`),
			wantErr: ErrReachedMaxInstances,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got, gotErr := parsePolicyConfig("pg", tt.pl)
			if tt.wantErr != nil {
				if gotErr == nil {
					t.Fatalf("expected non empty error, got=%v", tt.wantErr)
				}
				assert.EqualError(t, tt.wantErr, gotErr.Error(), tt.msg)
			}
			assert.Equal(t, tt.wantPolicy, got, tt.msg)
		})
	}
}
