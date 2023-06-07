package dcm

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/hashicorp/hcl/v2/hclsimple"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

var (
	errReachedMaxInstances     = fmt.Errorf("reached max instances (%v) per policy", maxPolicyInstances)
	errEmptyPolicyConfig       = errors.New("policy-config entry is empty")
	errMaxExpirationTime       = fmt.Errorf("the max configurable expiration time is %v", maxExpirationTime.String())
	hasValidDatabaseNameRegexp = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9\._$]*$`)
)

type PolicyConfig struct {
	Items []Policy `hcl:"policy,block"`
}

type Policy struct {
	Name              string   `hcl:"name,label"`
	Engine            string   `hcl:"engine"`
	PluginConfigEntry string   `hcl:"plugin_config_entry"`
	Instances         []string `hcl:"instances"`
	Expiration        string   `hcl:"expiration,optional"`
	GrantPrivileges   []string `hcl:"grant_privileges"`

	datasource string
}

// parsePolicyConfig
func parsePolicyConfig(connectionName string, pl *plugin.Plugin) (*Policy, error) {
	encPolicyConfigData := pl.Config.EnvVars[policyConfigKeyName]
	if encPolicyConfigData == "" {
		return nil, errEmptyPolicyConfig
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

	policies := map[string]Policy{}
	for _, pol := range config.Items {
		if _, ok := policies[pol.Name]; ok {
			return nil, fmt.Errorf("policy name %v already exists", pol.Name)
		}
		policies[pol.Name] = pol
	}
	var policyConfigName string
	for _, conn := range pl.Connections {
		if conn.Name == connectionName && len(conn.Config) > 0 {
			policyConfigName = conn.Config[0]
			found, ok := policies[policyConfigName]
			if ok {
				if err := validatePolicyConstraints(found); err != nil {
					return nil, err
				}

				datasourceConfig, err := parseDatasourceConfig(found.PluginConfigEntry, pl.Config.EnvVars)
				if err != nil {
					return nil, err
				}
				found.datasource = datasourceConfig
				if found.Expiration == "" {
					found.Expiration = defaultExpirationDuration.String()
				}
				return &found, nil
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

func validatePolicyConstraints(p Policy) error {
	if len(p.Instances) >= maxPolicyInstances {
		return errReachedMaxInstances
	}
	if exp, _ := time.ParseDuration(p.Expiration); exp > maxExpirationTime {
		return errMaxExpirationTime
	}
	// validate grant privileges
	privmap := map[string]any{}
	var repeatedPrivileges []string
	for _, priv := range p.GrantPrivileges {
		if _, ok := privmap[priv]; ok {
			repeatedPrivileges = append(repeatedPrivileges, priv)
			continue
		}
		privmap[priv] = nil
	}
	if len(repeatedPrivileges) > 0 {
		return fmt.Errorf("found repeated privilege(s) %v", repeatedPrivileges)
	}

	var nonAllowedPrivileges []string
	for requestPriv := range privmap {
		if _, ok := allowedGrantPrivileges[requestPriv]; !ok {
			nonAllowedPrivileges = append(nonAllowedPrivileges, requestPriv)
		}
	}
	if len(nonAllowedPrivileges) > 0 {
		sort.Strings(nonAllowedPrivileges)
		return fmt.Errorf("privileges %v are not allowed for this engine", nonAllowedPrivileges)
	}

	// validate instances
	instancesmap := map[string]any{}
	var repeatedInstances []string
	var instancesWithInvalidName []string
	for _, db := range p.Instances {
		if !hasValidDatabaseNameRegexp.MatchString(db) {
			instancesWithInvalidName = append(instancesWithInvalidName, db)
		}

		if _, ok := instancesmap[db]; ok {
			repeatedInstances = append(repeatedInstances, db)
			continue
		}
		instancesmap[db] = nil
	}

	switch {
	case len(instancesWithInvalidName) > 0:
		sort.Strings(instancesWithInvalidName)
		return fmt.Errorf("found instances that doesn't comply with constraint database name: %q", instancesWithInvalidName)
	case len(repeatedInstances) > 0:
		sort.Strings(repeatedInstances)
		return fmt.Errorf("found repeated instance(s) %v", repeatedInstances)
	}

	return nil
}

func newPolicyChecksum(p *Policy) (string, error) {
	d, err := pb.GobEncode(p)
	if err != nil {
		return "", fmt.Errorf("failed hashing policy, gob encode error=%v", err)
	}
	h := sha256.New()
	if _, err := h.Write(d); err != nil {
		return "", fmt.Errorf("failed hashing policy, err=%v", err)
	}
	bs := h.Sum(nil)
	return fmt.Sprintf("%x", bs), nil
}
