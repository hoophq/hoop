package dcm

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/runopsio/hoop/gateway/plugin"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

type PolicyConfig struct {
	Items []Policy `hcl:"policy,block"`
}

// https://github.com/hashicorp/hcl/tree/e54a1960efd6cdfe35ecb8cc098bed33cd6001a8/guide
// https://github.com/hashicorp/hcl/blob/e54a1960efd6cdfe35ecb8cc098bed33cd6001a8/guide/go_patterns.rst#L17
// https://github.com/hashicorp/hcl/blob/e54a1960efd6cdfe35ecb8cc098bed33cd6001a8/gohcl/doc.go#L23
type Policy struct {
	Name              string   `hcl:"name,label"`
	Engine            string   `hcl:"engine"`
	PluginConfigEntry string   `hcl:"plugin_config_entry"`
	Instances         []string `hcl:"instances"`
	RenewDuration     string   `hcl:"renew,optional"`
	GrantPrivileges   []string `hcl:"grant_privileges"`

	datasource string
}

// parsePolicyConfig
func parsePolicyConfig(connectionName string, pl *plugin.Plugin) (*Policy, error) {
	encPolicyConfigData := pl.Config.EnvVars["policy-config"]
	if encPolicyConfigData == "" {
		return nil, fmt.Errorf("policy config is empty")
	}

	policyConfigDataBytes, err := base64.StdEncoding.DecodeString(encPolicyConfigData)
	if err != nil {
		return nil, fmt.Errorf("failed decoding policy config: %v", err)
	}
	var config PolicyConfig
	err = hclsimple.Decode("policy.hcl", policyConfigDataBytes, nil, &config)
	if err != nil {
		return nil, err
	}

	polmap := map[string]map[string]Policy{}
	policies := map[string]*Policy{}
	for _, pol := range config.Items {
		if _, ok := policies[pol.Name]; ok {
			return nil, fmt.Errorf("policy name %v already exists", pol.Name)
		}

		policies[pol.Name] = &pol
		for _, dbname := range pol.Instances {
			// <plugin-entry>:<privileges>
			entryKey := fmt.Sprintf("%s:%s",
				pol.PluginConfigEntry,
				strings.Join(pol.GrantPrivileges, ","))
			if m, ok := polmap[dbname]; ok {
				if e, exists := m[entryKey]; exists {
					return nil, fmt.Errorf("found duplicated privileges %v=%v", pol.Name, e.Name)
				}
				m[entryKey] = pol
				continue
			}
			polmap[dbname] = map[string]Policy{
				entryKey: pol,
			}
		}
	}
	var policyConfigName string
	for _, conn := range pl.Connections {
		if conn.Name == connectionName && len(conn.Config) > 0 {
			policyConfigName = conn.Config[0]
			found, ok := policies[policyConfigName]
			if ok {
				datasourceConfig, err := parseDatasourceConfig(found.PluginConfigEntry, pl.Config.EnvVars)
				if err != nil {
					return nil, err
				}
				found.datasource = datasourceConfig
				return found, nil
			}
			break
		}
	}
	if policyConfigName == "" {
		return nil, fmt.Errorf("missing configuration policy for this connection")
	}

	return nil, fmt.Errorf("policy %q not found for this connection", policyConfigName)
}

func parseDatasourceConfig(pluginConfigEntry string, envvars map[string]string) (string, error) {
	masterDbCredEnc, ok := envvars[pluginConfigEntry]
	if !ok {
		return "", fmt.Errorf("failed retrieving database credentials, missing configuration entry for %v", pluginConfigEntry)
	}
	masterDbCredBytes, err := base64.StdEncoding.DecodeString(masterDbCredEnc)
	if err != nil {
		return "", plugintypes.InternalErr("failed decoding database credentials configuration", err)
	}
	return string(masterDbCredBytes), nil
}
