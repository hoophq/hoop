package services

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/guardrails"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"gorm.io/gorm"

	// The protocol registry is populated by the codec seam. Without it
	// daemon.Config.Validate reports every listener as an unsupported
	// protocol.
	_ "github.com/hoophq/hoop/sidecar/codec/all"
)

// The ticket does not make these configurable: a sidecar logs where a
// container platform collects, and serves admin on the port the compose
// stacks and ADR-0005 already use.
const (
	sidecarAuditFile   = "-"
	sidecarAdminListen = "0.0.0.0:19000"
	sidecarLogLevel    = "info"
	// sidecarMaskStrategy is alcatraz.StrategyRedact, written out rather
	// than relied on as the zero value.
	sidecarMaskStrategy = "redact"
)

// ErrSidecarNoConnections reports a sidecar nothing is assigned to. A config
// with no listeners is a silent outage, not an empty success.
var ErrSidecarNoConnections = errors.New("no connections are assigned to this sidecar")

// ErrSidecarLookup wraps a database failure. The caller answers 422 for an
// operator mistake and 500 for this: a sidecar that retries a 422 never
// recovers, and the driver text must not reach it.
var ErrSidecarLookup = errors.New("failed loading sidecar configuration")

// GenerateSidecarKey returns the token a sidecar authenticates with. The
// "hsc_" prefix keeps it out of the "hpk_" branch of the API auth middleware.
func GenerateSidecarKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	return "hsc_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// SidecarProtocol maps a hoop connection type/subtype onto a sidecar codec.
// It returns an error naming the connection type when no codec speaks it.
func SidecarProtocol(connType, subType string) (string, error) {
	switch pb.ToConnectionType(connType, subType) {
	case pb.ConnectionTypePostgres:
		return string(inspect.Postgres), nil
	case pb.ConnectionTypeMSSQL:
		return string(inspect.MSSQL), nil
	case pb.ConnectionTypeHttpProxy:
		return string(inspect.HTTP), nil
	}
	return "", fmt.Errorf("connection type %q cannot be served by a sidecar; supported: postgres, mssql, httpproxy",
		strings.TrimSpace(connType+"/"+subType))
}

// sidecarConnection bundles a connection row with the guardrail and masking
// rules already fetched for it, so the translation is a pure function of its
// input and can be tested without a database.
type sidecarConnection struct {
	name     string
	connType string
	subType  string
	envs     map[string]string

	guardRailInputRules  json.RawMessage
	guardRailOutputRules json.RawMessage
	dataMaskingRules     json.RawMessage
}

// dataMaskingRule is the shape GetDataMaskingEntityTypesByConnectionAndAttributes
// builds.
type dataMaskingRule struct {
	Name                 string                             `json:"name"`
	SupportedEntityTypes []models.SupportedEntityTypesEntry `json:"supported_entity_types"`
	CustomEntityTypes    []models.CustomEntityTypesEntry    `json:"custom_entity_types"`
	ScoreThreshold       *float64                           `json:"score_threshold"`
}

// BuildSidecarConfig assembles the config a sidecar serves, from the
// connections assigned to it and the guardrail and masking rules attached to
// each one. Nothing is stored: the config is rebuilt on every request, so a
// rule edited in the UI reaches the sidecar on its next fetch.
func BuildSidecarConfig(db *gorm.DB, orgID, sidecarID string) (*daemon.Config, error) {
	conns, err := models.ListConnectionsBySidecarID(db, orgID, sidecarID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSidecarLookup, err)
	}
	if len(conns) == 0 {
		return nil, ErrSidecarNoConnections
	}

	items := make([]sidecarConnection, 0, len(conns))
	for _, conn := range conns {
		item := sidecarConnection{
			name:     conn.Name,
			connType: conn.Type,
			subType:  conn.SubType.String,
			envs:     conn.Envs,
		}
		rules, err := GetGuardRailRulesForConnection(orgID, conn.Name)
		if err != nil {
			return nil, fmt.Errorf("%w: guardrail rules for connection %q: %v", ErrSidecarLookup, conn.Name, err)
		}
		if rules != nil {
			item.guardRailInputRules = json.RawMessage(rules.GuardRailInputRules)
			item.guardRailOutputRules = json.RawMessage(rules.GuardRailOutputRules)
		}
		maskRules, err := GetDataMaskingRulesForConnection(orgID, conn.Name)
		if err != nil {
			return nil, fmt.Errorf("%w: data masking rules for connection %q: %v", ErrSidecarLookup, conn.Name, err)
		}
		item.dataMaskingRules = maskRules
		items = append(items, item)
	}
	return buildConfig(items)
}

func buildConfig(conns []sidecarConnection) (*daemon.Config, error) {
	cfg := &daemon.Config{
		Audit:    daemon.AuditConfig{File: sidecarAuditFile},
		Admin:    daemon.AdminConfig{Listen: sidecarAdminListen},
		LogLevel: sidecarLogLevel,
	}

	var piiEntities []string
	seenEntity := map[string]bool{}
	var piiThreshold *float64

	for _, conn := range conns {
		protocol, err := SidecarProtocol(conn.connType, conn.subType)
		if err != nil {
			return nil, err
		}
		upstream, port, upstreamTLS, err := sidecarUpstream(conn, protocol)
		if err != nil {
			return nil, err
		}

		listener := daemon.ListenerConfig{
			Name:     conn.name,
			Protocol: protocol,
			Listen:   "0.0.0.0:" + port,
			Upstream: upstream,
		}
		// An empty, non-nil TLSConfig turns on upstream TLS with normal
		// certificate verification; startTLS derives the SNI server name from
		// the upstream address.
		if upstreamTLS {
			listener.UpstreamTLS = &daemon.TLSConfig{}
		}

		rules, err := sidecarGuardrails(conn)
		if err != nil {
			return nil, err
		}
		if len(rules) > 0 {
			listener.Guardrails = &daemon.GuardrailsConfig{Mode: daemon.ModeEnforce, Rules: rules}
		}

		maskRules, entities, threshold, err := sidecarMaskRules(conn)
		if err != nil {
			return nil, err
		}
		if len(maskRules) > 0 {
			raw, err := json.Marshal(maskRules)
			if err != nil {
				return nil, err
			}
			listener.Mask = &daemon.MaskConfig{Rules: raw}
		}
		for _, entity := range entities {
			if seenEntity[entity] {
				continue
			}
			seenEntity[entity] = true
			piiEntities = append(piiEntities, entity)
		}
		// The lowest threshold detects the most, which is the safe direction
		// when two connections disagree.
		if threshold != nil && (piiThreshold == nil || *threshold < *piiThreshold) {
			piiThreshold = threshold
		}

		cfg.Listeners = append(cfg.Listeners, listener)
	}

	if len(piiEntities) > 0 {
		section := map[string]any{"entities": piiEntities}
		if piiThreshold != nil {
			section["threshold"] = *piiThreshold
		}
		raw, err := json.Marshal(section)
		if err != nil {
			return nil, err
		}
		cfg.PII = raw
	}

	// Round-trip through the daemon's own decoder: DisallowUnknownFields,
	// normalize and Validate run, so the gateway never hands out a config
	// the sidecar would refuse.
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return daemon.LoadConfigBytes(encoded)
}

// sidecarUpstream resolves the backend address and the port the sidecar binds
// on: it takes over the address the application already dials, so nothing
// downstream needs reconfiguring.
func sidecarUpstream(conn sidecarConnection, protocol string) (upstream, port string, upstreamTLS bool, err error) {
	if protocol == string(inspect.HTTP) {
		remoteURL, err := decodeEnv(conn.envs, "REMOTE_URL")
		if err != nil {
			return "", "", false, fmt.Errorf("connection %q has an unreadable REMOTE_URL: %v", conn.name, err)
		}
		if remoteURL == "" {
			return "", "", false, fmt.Errorf("connection %q has no REMOTE_URL configured", conn.name)
		}
		u, err := url.Parse(remoteURL)
		if err != nil || u.Hostname() == "" {
			return "", "", false, fmt.Errorf("connection %q has an invalid REMOTE_URL configured", conn.name)
		}
		port = u.Port()
		if port == "" {
			port = "80"
			if u.Scheme == "https" {
				port = "443"
			}
		}
		if !isValidPort(port) {
			return "", "", false, fmt.Errorf("connection %q has an invalid port in REMOTE_URL", conn.name)
		}
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]" // IPv6 literal
		}
		return host + ":" + port, port, u.Scheme == "https", nil
	}

	host, err := decodeEnv(conn.envs, "HOST")
	if err != nil {
		return "", "", false, fmt.Errorf("connection %q has an unreadable HOST: %v", conn.name, err)
	}
	if host == "" {
		return "", "", false, fmt.Errorf("connection %q has no HOST configured", conn.name)
	}
	// The upstream is joined as HOST:PORT, so a HOST that already carries a
	// port, or a bare IPv6 literal, would build an address nothing can dial.
	// daemon.Validate only checks that the field is non-empty, so the dial
	// would fail at the first client connection instead of here.
	if _, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		return "", "", false, fmt.Errorf("connection %q has a HOST that already carries a port (%q); set HOST and PORT separately", conn.name, host)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6 literal
	}
	port, err = decodeEnv(conn.envs, "PORT")
	if err != nil {
		return "", "", false, fmt.Errorf("connection %q has an unreadable PORT: %v", conn.name, err)
	}
	if port == "" {
		return "", "", false, fmt.Errorf("connection %q has no PORT configured", conn.name)
	}
	if !isValidPort(port) {
		return "", "", false, fmt.Errorf("connection %q has an invalid PORT %q", conn.name, port)
	}
	return host + ":" + port, port, false, nil
}

// isValidPort rejects what strconv.Atoi accepts but a listener cannot bind:
// zero, negatives, a leading sign, and anything above the 16-bit range.
func isValidPort(port string) bool {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return false
	}
	return strconv.Itoa(n) == port
}

// decodeEnv reads a connection env-var secret, stored base64-encoded under
// the "envvar:NAME" key. An absent key returns an empty value; a key that is
// present but undecodable is an error, so the caller does not report a
// corrupt value as a missing one.
func decodeEnv(envs map[string]string, name string) (string, error) {
	enc := envs["envvar:"+name]
	if enc == "" {
		return "", nil
	}
	decoded, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("value is not valid base64")
	}
	return string(decoded), nil
}

func sidecarGuardrails(conn sidecarConnection) ([]policy.Rule, error) {
	if len(conn.guardRailOutputRules) > 0 {
		outputRules, err := guardrails.Decode(conn.guardRailOutputRules)
		if err != nil {
			return nil, fmt.Errorf("connection %q has undecodable guardrail output rules: %w", conn.name, err)
		}
		for _, dataRule := range outputRules {
			if len(dataRule.Items) > 0 {
				return nil, fmt.Errorf("connection %q has guardrail output rules, which a sidecar cannot enforce", conn.name)
			}
		}
	}
	if len(conn.guardRailInputRules) == 0 {
		return nil, nil
	}
	inputRules, err := guardrails.Decode(conn.guardRailInputRules)
	if err != nil {
		return nil, fmt.Errorf("connection %q has undecodable guardrail input rules: %w", conn.name, err)
	}

	var rules []policy.Rule
	for _, dataRule := range inputRules {
		for _, r := range dataRule.Items {
			// The gateway matcher treats these as no-ops; on the sidecar an
			// empty pattern_match would be a config error.
			switch r.Type {
			case string(policy.MatchDenyWords):
				words := nonEmpty(r.Words)
				if len(words) == 0 {
					continue
				}
				rules = append(rules, policy.Rule{
					Name:    fmt.Sprintf("%s-%d", conn.name, len(rules)),
					Type:    policy.MatchDenyWords,
					Words:   words,
					Message: r.Message,
				})
			case string(policy.MatchPattern):
				if r.PatternRegex == "" {
					continue
				}
				rules = append(rules, policy.Rule{
					Name:    fmt.Sprintf("%s-%d", conn.name, len(rules)),
					Type:    policy.MatchPattern,
					Pattern: r.PatternRegex,
					Message: r.Message,
				})
			default:
				return nil, fmt.Errorf("guardrail rule type %q is not supported by the sidecar", r.Type)
			}
		}
	}
	return rules, nil
}

func nonEmpty(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// sidecarMaskRules translates data masking rules into alcatraz mask rules,
// kept as plain maps because the daemon package does not link a detector and
// the gateway must not link the alcatraz module.
func sidecarMaskRules(conn sidecarConnection) (rules []map[string]any, entities []string, threshold *float64, err error) {
	if len(conn.dataMaskingRules) == 0 {
		return nil, nil, nil, nil
	}
	var decoded []dataMaskingRule
	if err := json.Unmarshal(conn.dataMaskingRules, &decoded); err != nil {
		return nil, nil, nil, fmt.Errorf("connection %q has undecodable data masking rules: %w", conn.name, err)
	}
	for _, rule := range decoded {
		if len(rule.CustomEntityTypes) > 0 {
			return nil, nil, nil, fmt.Errorf(
				"data masking rule %q uses custom entity types, which the sidecar detector cannot express", rule.Name)
		}
		var ruleEntities []string
		for _, entry := range rule.SupportedEntityTypes {
			ruleEntities = append(ruleEntities, entry.EntityTypes...)
		}
		if len(ruleEntities) == 0 {
			continue
		}
		rules = append(rules, map[string]any{
			"name":     rule.Name,
			"entities": ruleEntities,
			"strategy": sidecarMaskStrategy,
		})
		entities = append(entities, ruleEntities...)
		if rule.ScoreThreshold != nil && (threshold == nil || *rule.ScoreThreshold < *threshold) {
			threshold = rule.ScoreThreshold
		}
	}
	return rules, entities, threshold, nil
}
