package apireports

import (
	"fmt"
	"math"
	"strings"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

type checkResult struct {
	Status   openapi.ComplianceStatusType
	Message  string
	Evidence string
}

// cardTypes are the redact info types that indicate cardholder data masking.
var cardTypes = map[string]bool{
	"CREDIT_CARD_NUMBER": true,
	"CREDIT_CARD":        true,
	"CARD_NUMBER":        true,
	"PAN":                true,
}

const prodEnvironmentTagKey = "hoop.dev/infrastructure.environment"

func isProdConnection(c models.Connection) bool {
	if c.ConnectionTags[prodEnvironmentTagKey] == "prod" {
		return true
	}
	for _, t := range c.Tags {
		if t == "prod" {
			return true
		}
	}
	return strings.Contains(strings.ToLower(c.Name), "prod")
}

func percentOf(part, total int) int {
	if total == 0 {
		return 0
	}
	return int(math.Round(100 * float64(part) / float64(total)))
}

// evalComplianceChecks evaluates the 33 checks of the catalog against the
// snapshot. The returned map is keyed by check ID and always contains one
// entry per catalog check plus the pseudo-checks.
func evalComplianceChecks(snap *complianceSnapshot) map[string]checkResult {
	out := make(map[string]checkResult, len(complianceChecks)+3)
	ssoEnabled := snap.AuthMethod != "local"
	authEvidence := fmt.Sprintf("Authentication method: %s", strings.ToUpper(snap.AuthMethod))
	authMethodUpper := strings.ToUpper(snap.AuthMethod)

	// ---- identity ----
	if ssoEnabled {
		out["sso_enabled"] = checkResult{openapi.ComplianceStatusCompliant,
			fmt.Sprintf("SSO is enabled via %s provider", authMethodUpper), authEvidence}
		out["unique_user_ids"] = checkResult{openapi.ComplianceStatusCompliant,
			"Users are uniquely identified via SSO provider", authEvidence}
		out["auth_method_strength"] = checkResult{openapi.ComplianceStatusCompliant,
			"All user access requires authentication through your configured provider", authEvidence}
		out["mfa_status"] = checkResult{openapi.ComplianceStatusIdpDependent,
			"MFA enforcement delegated to Identity Provider. Verify MFA is required in IdP settings.", authEvidence}
	} else {
		out["sso_enabled"] = checkResult{openapi.ComplianceStatusWarning,
			"Using local authentication. Consider enabling SSO for enterprise security.", authEvidence}
		out["unique_user_ids"] = checkResult{openapi.ComplianceStatusWarning,
			"Local authentication in use. SSO recommended for unique user identification.", authEvidence}
		out["auth_method_strength"] = checkResult{openapi.ComplianceStatusWarning,
			"Using local authentication. Consider enabling SSO for enterprise security.", authEvidence}
		out["mfa_status"] = checkResult{openapi.ComplianceStatusWarning,
			"Local authentication does not support MFA. Configure SSO with an MFA-enabled IdP.", authEvidence}
	}

	// ---- access_control ----
	groups := len(snap.GroupNames)
	groupsEvidence := fmt.Sprintf("%d groups configured", groups)
	switch {
	case groups >= 3:
		out["rbac_groups"] = checkResult{openapi.ComplianceStatusCompliant,
			fmt.Sprintf("%d user groups enable role-based access control", groups), groupsEvidence}
	case groups >= 1:
		out["rbac_groups"] = checkResult{openapi.ComplianceStatusWarning,
			fmt.Sprintf("Only %d groups defined. Consider more granular access control.", groups), groupsEvidence}
	default:
		out["rbac_groups"] = checkResult{openapi.ComplianceStatusNonCompliant,
			"No user groups defined. Access cannot be restricted by role.", groupsEvidence}
	}

	restrictedConns := 0
	reviewedConns := 0
	for _, c := range snap.Connections {
		if len(c.Reviewers) > 0 || len(c.GuardRailRules) > 0 {
			restrictedConns++
		}
		if len(c.Reviewers) > 0 {
			reviewedConns++
		}
	}
	rbaEvidence := fmt.Sprintf("%d groups, %d connections with access restrictions", groups, restrictedConns)
	switch {
	case groups > 0 && restrictedConns > 0:
		out["role_based_access"] = checkResult{openapi.ComplianceStatusCompliant,
			"User groups and connection-level access restrictions are configured", rbaEvidence}
	case groups > 0:
		out["role_based_access"] = checkResult{openapi.ComplianceStatusWarning,
			"User groups exist but connection-level access restrictions recommended", rbaEvidence}
	default:
		out["role_based_access"] = checkResult{openapi.ComplianceStatusNonCompliant,
			"No user groups or connection-level access restrictions configured", rbaEvidence}
	}

	prodConns, prodCovered := 0, 0
	for _, c := range snap.Connections {
		if isProdConnection(c) {
			prodConns++
			if len(c.Reviewers) > 0 {
				prodCovered++
			}
		}
	}
	if prodConns == 0 {
		out["jit_reviews"] = checkResult{openapi.ComplianceStatusWarning,
			"No production connections identified. Tag connections for better visibility.",
			fmt.Sprintf("%d connections with reviewers", reviewedConns)}
	} else {
		jitEvidence := fmt.Sprintf("%d/%d production connections protected", prodCovered, prodConns)
		coverage := percentOf(prodCovered, prodConns)
		switch {
		case coverage == 100:
			out["jit_reviews"] = checkResult{openapi.ComplianceStatusCompliant,
				"All production connections require just-in-time review approval", jitEvidence}
		case coverage >= 50:
			out["jit_reviews"] = checkResult{openapi.ComplianceStatusWarning,
				fmt.Sprintf("Only %d%% of production connections require review approval", coverage), jitEvidence}
		default:
			out["jit_reviews"] = checkResult{openapi.ComplianceStatusNonCompliant,
				fmt.Sprintf("Only %d%% of production connections require review approval", coverage), jitEvidence}
		}
	}

	saCount := len(snap.ServiceAccounts)
	if saCount > 0 {
		out["service_accounts_managed"] = checkResult{openapi.ComplianceStatusCompliant,
			fmt.Sprintf("%d service accounts inventoried for privileged access review", saCount),
			fmt.Sprintf("%d service accounts configured", saCount)}
	} else {
		out["service_accounts_managed"] = checkResult{openapi.ComplianceStatusNotApplicable,
			"No service accounts configured", "0 service accounts configured"}
	}

	lpEvidence := fmt.Sprintf("%d groups, %d connections with reviewers", groups, reviewedConns)
	switch {
	case groups >= 3 && reviewedConns > 0:
		out["least_privilege"] = checkResult{openapi.ComplianceStatusCompliant,
			"Granular groups and connection review requirements enforce least privilege", lpEvidence}
	case groups > 0:
		out["least_privilege"] = checkResult{openapi.ComplianceStatusWarning,
			"Partial least privilege setup. Add more groups and connection review requirements.", lpEvidence}
	default:
		out["least_privilege"] = checkResult{openapi.ComplianceStatusNonCompliant,
			"No user groups configured. Least privilege cannot be enforced.", lpEvidence}
	}

	activeUsers := 0
	for _, u := range snap.Users {
		if u.Status == "active" {
			activeUsers++
		}
	}
	out["user_access_reviews"] = checkResult{openapi.ComplianceStatusCompliant,
		"User management enables periodic access reviews with status tracking",
		fmt.Sprintf("%d users (%d active), user management available for periodic review", len(snap.Users), activeUsers)}

	// ---- data_protection ----
	maskedConns := 0
	for _, c := range snap.Connections {
		if c.RedactEnabled {
			maskedConns++
		}
	}
	switch {
	case maskedConns > 0:
		out["masking_enabled"] = checkResult{openapi.ComplianceStatusCompliant,
			fmt.Sprintf("Data masking active on %d connections", maskedConns),
			fmt.Sprintf("%d/%d connections with masking enabled", maskedConns, len(snap.Connections))}
	case len(snap.Connections) > 0:
		out["masking_enabled"] = checkResult{openapi.ComplianceStatusNonCompliant,
			"No connections have data masking enabled",
			fmt.Sprintf("0/%d connections with masking enabled", len(snap.Connections))}
	default:
		out["masking_enabled"] = checkResult{openapi.ComplianceStatusNotApplicable,
			"No connections configured", "0 connections"}
	}

	var databases, maskedDBs int
	hasCardTypes := false
	distinctRedactTypes := map[string]bool{}
	for _, c := range snap.Connections {
		if c.RedactEnabled {
			for _, t := range c.RedactTypes {
				distinctRedactTypes[t] = true
			}
		}
		if c.Type != "database" {
			continue
		}
		databases++
		if c.RedactEnabled {
			maskedDBs++
			for _, t := range c.RedactTypes {
				if cardTypes[t] {
					hasCardTypes = true
				}
			}
		}
	}
	dbEvidence := fmt.Sprintf("%d/%d databases protected", maskedDBs, databases)
	dbCoverage := percentOf(maskedDBs, databases)
	switch {
	case databases == 0:
		out["masking_coverage"] = checkResult{openapi.ComplianceStatusNotApplicable,
			"No database connections configured", "0 database connections"}
	case dbCoverage == 100:
		out["masking_coverage"] = checkResult{openapi.ComplianceStatusCompliant,
			fmt.Sprintf("All %d database connections have data masking enabled", databases), dbEvidence}
	case dbCoverage >= 80:
		out["masking_coverage"] = checkResult{openapi.ComplianceStatusWarning,
			fmt.Sprintf("Only %d%% of databases have masking enabled", dbCoverage), dbEvidence}
	default:
		out["masking_coverage"] = checkResult{openapi.ComplianceStatusNonCompliant,
			fmt.Sprintf("Only %d%% of databases have masking enabled", dbCoverage), dbEvidence}
	}

	switch {
	case len(distinctRedactTypes) > 0:
		out["sensitive_types_configured"] = checkResult{openapi.ComplianceStatusCompliant,
			"Sensitive data types are configured for automatic detection",
			fmt.Sprintf("%d distinct info types configured", len(distinctRedactTypes))}
	case maskedConns > 0:
		out["sensitive_types_configured"] = checkResult{openapi.ComplianceStatusWarning,
			"Masking enabled but detection types not confirmed",
			fmt.Sprintf("%d masked connections, 0 info types resolved", maskedConns)}
	default:
		out["sensitive_types_configured"] = checkResult{openapi.ComplianceStatusNonCompliant,
			"No data masking configured. Sensitive data types are not detected.",
			"0 masked connections"}
	}

	switch {
	case databases == 0:
		out["chd_masking_types"] = checkResult{openapi.ComplianceStatusNotApplicable,
			"No database connections configured", "0 database connections"}
	case dbCoverage == 100 && hasCardTypes:
		out["chd_masking_types"] = checkResult{openapi.ComplianceStatusCompliant,
			"All databases are masked with card-specific info types configured", dbEvidence}
	case dbCoverage == 100:
		out["chd_masking_types"] = checkResult{openapi.ComplianceStatusWarning,
			"Masking enabled but card-specific info types not confirmed", dbEvidence}
	default:
		mc := out["masking_coverage"]
		out["chd_masking_types"] = checkResult{mc.Status, mc.Message, mc.Evidence}
	}

	guardrailConns := 0
	for _, c := range snap.Connections {
		if len(c.GuardRailRules) > 0 {
			guardrailConns++
		}
	}
	if snap.GuardrailCount > 0 {
		out["guardrails_active"] = checkResult{openapi.ComplianceStatusCompliant,
			fmt.Sprintf("%d guardrail rules protecting your infrastructure", snap.GuardrailCount),
			fmt.Sprintf("%d rules, %d connections with guardrails", snap.GuardrailCount, guardrailConns)}
	} else {
		out["guardrails_active"] = checkResult{openapi.ComplianceStatusWarning,
			"No guardrail rules configured. Consider adding command filters.",
			"0 guardrail rules"}
	}

	out["transmission_encryption"] = checkResult{openapi.ComplianceStatusCompliant,
		"All data transmission is secured with TLS via gRPC tunnels",
		"TLS encryption: enforced by architecture"}

	maskingCompliant := out["masking_enabled"].Status == openapi.ComplianceStatusCompliant
	dmEvidence := fmt.Sprintf("%d masked connections, %d groups", maskedConns, groups)
	switch {
	case maskingCompliant && groups > 0:
		out["data_minimization"] = checkResult{openapi.ComplianceStatusCompliant,
			"Data masking and role-based access minimize data exposure", dmEvidence}
	case maskingCompliant || groups > 0:
		out["data_minimization"] = checkResult{openapi.ComplianceStatusWarning,
			"Partial data minimization. Enable both masking and user groups.", dmEvidence}
	default:
		out["data_minimization"] = checkResult{openapi.ComplianceStatusNonCompliant,
			"No data masking or user groups configured", dmEvidence}
	}

	// ---- audit_trail ----
	out["session_recording"] = checkResult{openapi.ComplianceStatusCompliant,
		"All sessions are automatically recorded with full audit trail",
		"Session recording: Enabled (default)"}
	out["audit_log_details"] = checkResult{openapi.ComplianceStatusCompliant,
		"Audit logs capture complete session details",
		"Captured: user identity, event type, timestamp, status, connection, subtype"}

	var totalSessions int64
	if snap.SessionMetrics != nil {
		totalSessions = snap.SessionMetrics.TotalSessions
	}
	out["user_activity_logged"] = checkResult{openapi.ComplianceStatusCompliant,
		"All user activity is logged in session records",
		fmt.Sprintf("%d sessions recorded in the last 30 days", totalSessions)}
	out["admin_actions_logged"] = checkResult{openapi.ComplianceStatusCompliant,
		"Administrative and privileged actions are captured in session logs",
		fmt.Sprintf("%d sessions recorded in the last 30 days", totalSessions)}
	out["session_integrity"] = checkResult{openapi.ComplianceStatusCompliant,
		"Sessions are immutable after creation",
		"Session records: immutable by design"}
	out["log_retention"] = checkResult{openapi.ComplianceStatusUnableToVerify,
		"Audit log retention depends on your deployment and storage configuration",
		"Not verifiable from gateway data"}

	// ---- monitoring_response ----
	if snap.WebhookConfigured {
		out["siem_integration"] = checkResult{openapi.ComplianceStatusCompliant,
			"Webhook integration configured for security event forwarding",
			"Webhook integration: configured"}
		out["automated_log_review"] = checkResult{openapi.ComplianceStatusCompliant,
			"Webhook integration enables automated audit log analysis",
			"Webhook integration: configured"}
		out["security_event_alerts"] = checkResult{openapi.ComplianceStatusCompliant,
			"Security events are forwarded for external alerting",
			"Webhook integration: configured"}
	} else {
		out["siem_integration"] = checkResult{openapi.ComplianceStatusWarning,
			"No SIEM/webhook integration. Security events are not forwarded externally.",
			"Webhook integration: not configured"}
		out["automated_log_review"] = checkResult{openapi.ComplianceStatusWarning,
			"No webhook integration. Audit logs are not automatically analyzed externally.",
			"Webhook integration: not configured"}
		out["security_event_alerts"] = checkResult{openapi.ComplianceStatusWarning,
			"No SIEM/webhook integration. Security events are not forwarded externally.",
			"Webhook integration: not configured"}
	}

	if totalSessions > 0 {
		out["activity_monitoring"] = checkResult{openapi.ComplianceStatusCompliant,
			fmt.Sprintf("%d sessions available for review (last 30 days)", totalSessions),
			fmt.Sprintf("%d sessions in the last 30 days", totalSessions)}
	} else {
		out["activity_monitoring"] = checkResult{openapi.ComplianceStatusWarning,
			"No recent session activity to review",
			"0 sessions in the last 30 days"}
	}

	if snap.PendingReviews == 0 {
		out["review_response_sla"] = checkResult{openapi.ComplianceStatusCompliant,
			"No pending access reviews", "0 pending reviews"}
	} else {
		out["review_response_sla"] = checkResult{openapi.ComplianceStatusWarning,
			fmt.Sprintf("%d access reviews pending response", snap.PendingReviews),
			fmt.Sprintf("%d pending reviews", snap.PendingReviews)}
	}

	// ---- infrastructure ----
	totalAgents, connectedAgents := len(snap.Agents), 0
	for _, a := range snap.Agents {
		if a.Status == string(models.AgentStatusConnected) {
			connectedAgents++
		}
	}
	agentEvidence := fmt.Sprintf("%d/%d agents online", connectedAgents, totalAgents)
	var agentHealth checkResult
	if totalAgents == 0 {
		agentHealth = checkResult{openapi.ComplianceStatusWarning,
			"No agents configured. Deploy agents to connect resources.", agentEvidence}
	} else {
		agentCoverage := percentOf(connectedAgents, totalAgents)
		disconnected := totalAgents - connectedAgents
		switch {
		case agentCoverage == 100:
			agentHealth = checkResult{openapi.ComplianceStatusCompliant,
				fmt.Sprintf("All %d agents connected and healthy", totalAgents), agentEvidence}
		case agentCoverage >= 90:
			agentHealth = checkResult{openapi.ComplianceStatusWarning,
				fmt.Sprintf("%d agent(s) disconnected", disconnected), agentEvidence}
		default:
			agentHealth = checkResult{openapi.ComplianceStatusNonCompliant,
				"Multiple agents disconnected. Infrastructure at risk.", agentEvidence}
		}
	}
	out["agents_online"] = agentHealth
	out["agent_health"] = agentHealth

	if snap.GatewayVersion == "unknown" || totalAgents == 0 {
		out["agent_version_current"] = checkResult{openapi.ComplianceStatusUnableToVerify,
			"Agent version currency cannot be verified",
			fmt.Sprintf("Gateway version: %s, %d agents", snap.GatewayVersion, totalAgents)}
	} else {
		outdated := 0
		versions := make([]string, 0, totalAgents)
		for _, a := range snap.Agents {
			v := a.GetMeta("version")
			versions = append(versions, v)
			if v != snap.GatewayVersion {
				outdated++
			}
		}
		versionEvidence := fmt.Sprintf("Gateway: %s, agents: %s", snap.GatewayVersion, strings.Join(versions, ", "))
		if outdated == 0 {
			out["agent_version_current"] = checkResult{openapi.ComplianceStatusCompliant,
				"All agents are running the gateway version", versionEvidence}
		} else {
			out["agent_version_current"] = checkResult{openapi.ComplianceStatusWarning,
				fmt.Sprintf("%d agent(s) running a different version than the gateway", outdated), versionEvidence}
		}
	}

	switch {
	case connectedAgents > 0:
		out["secure_tunnel"] = checkResult{openapi.ComplianceStatusCompliant,
			"Agents connected via TLS-encrypted gRPC tunnel", agentEvidence}
	case totalAgents > 0:
		out["secure_tunnel"] = checkResult{openapi.ComplianceStatusWarning,
			"Agents configured but none connected", agentEvidence}
	default:
		out["secure_tunnel"] = checkResult{openapi.ComplianceStatusWarning,
			"Awaiting agent deployment", agentEvidence}
	}

	if connectedAgents > 0 {
		out["system_availability"] = checkResult{openapi.ComplianceStatusCompliant,
			"System availability confirmed via connected agents", agentEvidence}
	} else {
		out["system_availability"] = checkResult{openapi.ComplianceStatusWarning,
			"No connected agents. System availability cannot be confirmed.", agentEvidence}
	}

	// ---- pseudo-checks (framework-only rows) ----
	out[pseudoCheckIdpDelegated] = checkResult{openapi.ComplianceStatusIdpDependent,
		"Delegated to your Identity Provider. Verify this setting in your IdP admin console.",
		fmt.Sprintf("Auth method: %s", authMethodUpper)}
	out[pseudoCheckInfraDelegated] = checkResult{openapi.ComplianceStatusUnableToVerify,
		"Depends on your deployment configuration. Verify in your infrastructure.",
		"Not verifiable from gateway data"}
	out[pseudoCheckGapNone] = checkResult{openapi.ComplianceStatusNotApplicable,
		"Not covered by Hoop in this version.", ""}

	return out
}
