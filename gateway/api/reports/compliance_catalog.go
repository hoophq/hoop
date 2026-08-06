package apireports

// Category identifiers and display titles, in fixed report order.
const (
	categoryIdentity           = "identity"
	categoryAccessControl      = "access_control"
	categoryDataProtection     = "data_protection"
	categoryAuditTrail         = "audit_trail"
	categoryMonitoringResponse = "monitoring_response"
	categoryInfrastructure     = "infrastructure"
)

var complianceCategories = []struct {
	ID    string
	Title string
}{
	{categoryIdentity, "Identity"},
	{categoryAccessControl, "Access Control"},
	{categoryDataProtection, "Data Protection"},
	{categoryAuditTrail, "Audit Trail"},
	{categoryMonitoringResponse, "Monitoring & Response"},
	{categoryInfrastructure, "Infrastructure"},
}

// Pseudo-check IDs used only by framework control rows. They yield constant
// results and are excluded from category summaries and action-required lists.
const (
	pseudoCheckIdpDelegated   = "idp_delegated"
	pseudoCheckInfraDelegated = "infra_delegated"
	pseudoCheckGapNone        = "gap_none"
)

type complianceCheckDef struct {
	ID       string
	Title    string
	Category string
}

// complianceChecks is the ordered catalog of the 33 evaluated checks.
var complianceChecks = []complianceCheckDef{
	// identity
	{"sso_enabled", "Single Sign-On Enabled", categoryIdentity},
	{"unique_user_ids", "Unique User Identification", categoryIdentity},
	{"auth_method_strength", "Authentication Method", categoryIdentity},
	{"mfa_status", "Multi-Factor Authentication", categoryIdentity},
	// access_control
	{"rbac_groups", "User Groups Defined", categoryAccessControl},
	{"role_based_access", "Role-Based Access", categoryAccessControl},
	{"jit_reviews", "Just-in-Time Access Reviews", categoryAccessControl},
	{"service_accounts_managed", "Service Accounts Managed", categoryAccessControl},
	{"least_privilege", "Least Privilege", categoryAccessControl},
	{"user_access_reviews", "User Access Reviews", categoryAccessControl},
	// data_protection
	{"masking_enabled", "AI Data Masking Enabled", categoryDataProtection},
	{"masking_coverage", "Database Masking Coverage", categoryDataProtection},
	{"sensitive_types_configured", "Sensitive Types Configured", categoryDataProtection},
	{"chd_masking_types", "CHD Masking Types (PCI)", categoryDataProtection},
	{"guardrails_active", "Command Guardrails Active", categoryDataProtection},
	{"transmission_encryption", "Transmission Encryption", categoryDataProtection},
	{"data_minimization", "Data Minimization", categoryDataProtection},
	// audit_trail
	{"session_recording", "Session Recording", categoryAuditTrail},
	{"audit_log_details", "Audit Log Details", categoryAuditTrail},
	{"user_activity_logged", "User Activity Logged", categoryAuditTrail},
	{"admin_actions_logged", "Admin Actions Logged", categoryAuditTrail},
	{"session_integrity", "Session Integrity", categoryAuditTrail},
	{"log_retention", "Log Retention", categoryAuditTrail},
	// monitoring_response
	{"siem_integration", "SIEM Integration", categoryMonitoringResponse},
	{"automated_log_review", "Automated Log Review", categoryMonitoringResponse},
	{"activity_monitoring", "Activity Monitoring", categoryMonitoringResponse},
	{"security_event_alerts", "Security Event Alerts", categoryMonitoringResponse},
	{"review_response_sla", "Review Response SLA", categoryMonitoringResponse},
	// infrastructure
	{"agents_online", "Agents Online", categoryInfrastructure},
	{"agent_health", "Agent Health", categoryInfrastructure},
	{"agent_version_current", "Agent Version Current", categoryInfrastructure},
	{"secure_tunnel", "Secure Tunnel Active", categoryInfrastructure},
	{"system_availability", "System Availability", categoryInfrastructure},
}

var complianceCheckByID = func() map[string]complianceCheckDef {
	m := make(map[string]complianceCheckDef, len(complianceChecks))
	for _, c := range complianceChecks {
		m[c.ID] = c
	}
	return m
}()

// complianceAction is internal remediation metadata. It is not exposed in the
// API payload; its Type drives the action-required filter (app/docs are
// actionable in-product, external/none are not).
type complianceAction struct {
	Label  string
	Type   string // app, docs, external, none
	Target string
}

type complianceControlDef struct {
	ID          string
	Title       string
	Description string
	CheckID     string
	Action      complianceAction
}

type complianceGroupDef struct {
	ID       string
	Title    string
	Controls []complianceControlDef
}

type complianceFrameworkDef struct {
	ID     string
	Name   string
	Groups []complianceGroupDef
}

// Shared remediation actions.
var (
	actionDocsIdentityProviders = complianceAction{Label: "Go to Docs ↗", Type: "docs", Target: "/docs/identity-providers"}
	actionDocsSessionRecording  = complianceAction{Label: "Learn more ↗", Type: "docs", Target: "/docs/session-recording"}
	actionDocsArchitecture      = complianceAction{Label: "Learn more ↗", Type: "docs", Target: "/docs/architecture"}
	actionDocsDataMasking       = complianceAction{Label: "Go to Docs ↗", Type: "docs", Target: "/docs/data-masking"}
	actionDocsDataMaskingLearn  = complianceAction{Label: "Learn more ↗", Type: "docs", Target: "/docs/data-masking"}
	actionDocsAccessControl     = complianceAction{Label: "Go to Docs ↗", Type: "docs", Target: "/docs/access-control"}
	actionDocsAgent             = complianceAction{Label: "Go to Docs ↗", Type: "docs", Target: "/docs/agent"}
	actionAppUsers              = complianceAction{Label: "Go to Users ↗", Type: "app", Target: "/organization/users"}
	actionAppResources          = complianceAction{Label: "Go to Resources ↗", Type: "app", Target: "/resources"}
	actionAppSessions           = complianceAction{Label: "Go to Sessions ↗", Type: "app", Target: "/sessions"}
	actionAppAgents             = complianceAction{Label: "Go to Agents ↗", Type: "app", Target: "/agents"}
	actionAppWebhooks           = complianceAction{Label: "Go to Webhooks ↗", Type: "app", Target: "/integrations/webhooks"}
	actionAppGuardrails         = complianceAction{Label: "Go to Guardrails ↗", Type: "app", Target: "/guardrails"}
	actionAppServiceAccounts    = complianceAction{Label: "Go to Service Accounts ↗", Type: "app", Target: "/organization/service-accounts"}
	actionAppReviews            = complianceAction{Label: "Go to Reviews ↗", Type: "app", Target: "/reviews"}
	actionExternalIdP           = complianceAction{Label: "Verify in IdP ↗", Type: "external", Target: ""}
	actionExternalInfra         = complianceAction{Label: "Verify in Infrastructure ↗", Type: "external", Target: ""}
	actionNone                  = complianceAction{Label: "—", Type: "none", Target: ""}
)

var complianceFrameworks = []complianceFrameworkDef{
	{
		ID:   "soc2",
		Name: "SOC 2 Type II",
		Groups: []complianceGroupDef{
			{
				ID:    "CC6",
				Title: "Logical and Physical Access Controls",
				Controls: []complianceControlDef{
					{"CC6.1", "Logical Access Security", "Single Sign-On is enabled, ensuring secure access to infrastructure through your Identity Provider", "sso_enabled", actionDocsIdentityProviders},
					{"CC6.1.a", "User Identification", "All users are identified and authenticated through your configured Identity Provider", "auth_method_strength", actionDocsIdentityProviders},
					{"CC6.2", "User Registration", "New users are registered and authorized through centralized user management", "user_access_reviews", actionAppUsers},
					{"CC6.3", "Access Removal", "User access can be deactivated when no longer needed, with status tracking for compliance", "user_access_reviews", actionAppUsers},
					{"CC6.6", "Role-Based Access", "Access to resources is restricted based on job function using user groups", "rbac_groups", actionAppUsers},
					{"CC6.7", "Access Rights Review", "Resource roles have group restrictions configured, enabling periodic access reviews", "role_based_access", actionAppResources},
				},
			},
			{
				ID:    "CC7",
				Title: "System Operations",
				Controls: []complianceControlDef{
					{"CC7.2", "System Monitoring", "All sessions are automatically recorded, enabling detection of anomalies and unauthorized access", "session_recording", actionDocsSessionRecording},
					{"CC7.2.a", "Unauthorized Access Detection", "Every session captures user identity, timestamps, and actions for complete audit trail", "audit_log_details", actionAppSessions},
					{"CC7.4", "Incident Response", "Sessions can be downloaded and replayed for forensic investigation of security incidents", "user_activity_logged", actionAppSessions},
				},
			},
			{
				ID:    "CC8",
				Title: "Change Management",
				Controls: []complianceControlDef{
					{"CC8.1", "Change Authorization", "Just-in-time reviews require approval before accessing sensitive resources", "jit_reviews", actionAppResources},
				},
			},
			{
				ID:    "P4",
				Title: "Privacy: Disclosure and Consent",
				Controls: []complianceControlDef{
					{"P4.1", "PII Protection Notice", "AI Data Masking automatically detects and redacts personally identifiable information", "masking_enabled", actionAppResources},
					{"P4.2", "Data Masking Transparency", "Users can see when data masking is applied, ensuring transparency in data handling", "sensitive_types_configured", actionDocsDataMaskingLearn},
				},
			},
		},
	},
	{
		ID:   "gdpr",
		Name: "GDPR",
		Groups: []complianceGroupDef{
			{
				ID:    "Art5",
				Title: "Principles Relating to Processing",
				Controls: []complianceControlDef{
					{"Art 5(1)(c)", "Data Minimization", "Role-based access ensures users only access the data necessary for their job function", "data_minimization", actionAppResources},
					{"Art 5(1)(d)", "Data Accuracy", "Direct connection model ensures real-time data access without stale copies", "transmission_encryption", actionDocsArchitecture},
					{"Art 5(1)(f)", "Integrity & Confidentiality", "AI Data Masking and TLS encryption protect data integrity and confidentiality", "masking_enabled", actionAppResources},
					{"Art 5(2)", "Accountability", "Complete audit logs of all sessions provide accountability for data processing activities", "session_recording", actionAppSessions},
				},
			},
			{
				ID:    "Art25",
				Title: "Data Protection by Design",
				Controls: []complianceControlDef{
					{"Art 25(1)", "Technical Measures", "AI Data Masking is enabled on your resource roles, protecting personal data by design", "masking_coverage", actionAppResources},
					{"Art 25(2)", "Minimization by Default", "Default sensitive data types are configured for automatic detection and masking", "sensitive_types_configured", actionDocsDataMasking},
				},
			},
			{
				ID:    "Art30",
				Title: "Records of Processing Activities",
				Controls: []complianceControlDef{
					{"Art 30(1)", "Processing Records", "All data access sessions are automatically recorded and stored for compliance", "session_recording", actionDocsSessionRecording},
					{"Art 30(1)(d)", "Data Categories", "Sensitive data types are detected and categorized during each session", "activity_monitoring", actionAppSessions},
				},
			},
			{
				ID:    "Art32",
				Title: "Security of Processing",
				Controls: []complianceControlDef{
					{"Art 32(1)(a)", "Pseudonymisation", "AI Data Masking pseudonymises sensitive data, with redaction counts tracked per session", "masking_coverage", actionAppResources},
					{"Art 32(1)(b)", "Ongoing Confidentiality", "TLS encryption and access control groups maintain ongoing data confidentiality", "role_based_access", actionDocsAccessControl},
					{"Art 32(1)(c)", "Availability", "Agent connectivity is monitored to ensure system availability and resilience", "agents_online", actionAppAgents},
					{"Art 32(1)(d)", "Regular Testing", "Regular security testing is not currently available as a built-in feature", pseudoCheckGapNone, actionNone},
				},
			},
			{
				ID:    "Art33",
				Title: "Breach Notification",
				Controls: []complianceControlDef{
					{"Art 33(1)", "Breach Notification", "SIEM/Webhook integration enables rapid detection and notification of security events", "siem_integration", actionAppWebhooks},
				},
			},
		},
	},
	{
		ID:   "pci_dss",
		Name: "PCI DSS 4.0",
		Groups: []complianceGroupDef{
			{
				ID:    "Req1",
				Title: "Network Security Controls",
				Controls: []complianceControlDef{
					{"1.2.1", "NSC Configuration Standards", "Guardrail rules define command filtering standards to protect cardholder data environments", "guardrails_active", actionAppGuardrails},
					{"1.3.1", "Inbound Traffic Restriction", "Group-based access controls restrict which users can access cardholder data resources", "role_based_access", actionAppResources},
					{"1.4.1", "Network Segmentation", "Secure gRPC tunnels provide network segmentation between trusted and untrusted zones", "secure_tunnel", actionDocsArchitecture},
				},
			},
			{
				ID:    "Req3",
				Title: "Protect Stored Account Data",
				Controls: []complianceControlDef{
					{"3.3.1", "SAD Not Retained", "AI Data Masking prevents sensitive authentication data from being retained or displayed", "masking_enabled", actionAppResources},
					{"3.3.2", "SAD Encryption", "Real-time masking encrypts sensitive data before it can be viewed or stored", "masking_enabled", actionAppResources},
					{"3.4.1", "PAN Masking", "Primary Account Numbers are automatically detected and masked when displayed", "chd_masking_types", actionDocsDataMasking},
					{"3.5.1", "PAN Unreadable", "AI Data Masking renders PAN unreadable across all database resource roles", "chd_masking_types", actionAppResources},
				},
			},
			{
				ID:    "Req4",
				Title: "Cryptography During Transmission",
				Controls: []complianceControlDef{
					{"4.2.1", "Transmission Encryption", "All data transmission is secured with TLS encryption via gRPC tunnels", "transmission_encryption", actionDocsArchitecture},
					{"4.2.1.1", "Certificate Validity", "TLS certificate validity monitoring requires infrastructure-level verification", pseudoCheckInfraDelegated, actionExternalInfra},
					{"4.2.2", "End-User Messaging", "Not applicable - Hoop.dev uses direct connections, not end-user messaging", pseudoCheckGapNone, actionNone},
				},
			},
			{
				ID:    "Req7",
				Title: "Restrict Access by Business Need",
				Controls: []complianceControlDef{
					{"7.1", "Access Restriction Processes", "User groups define and enforce access restriction policies across your organization", "rbac_groups", actionAppUsers},
					{"7.2.1", "Access Control Model", "Role-based access control model is implemented through user group configuration", "rbac_groups", actionAppUsers},
					{"7.2.2", "Job-Based Access", "Resource role access is assigned based on job classification using group restrictions", "role_based_access", actionAppResources},
					{"7.2.4", "Periodic Access Review", "User accounts and access privileges can be reviewed through user management", "user_access_reviews", actionAppUsers},
					{"7.2.5", "System Account Review", "Service accounts are inventoried and tracked for privileged access management", "service_accounts_managed", actionAppServiceAccounts},
					{"7.2.6", "CHD Query Restriction", "AI Data Masking restricts user access to stored cardholder data in query results", "chd_masking_types", actionAppResources},
				},
			},
			{
				ID:    "Req8",
				Title: "Identify and Authenticate Users",
				Controls: []complianceControlDef{
					{"8.2.1", "Unique User IDs", "SSO integration ensures every user has a unique identifier before system access", "unique_user_ids", actionDocsIdentityProviders},
					{"8.2.2", "No Shared Accounts", "Individual authentication via Identity Provider eliminates shared account usage", "unique_user_ids", actionDocsIdentityProviders},
					{"8.3.1", "User Authentication", "All user access requires authentication through your configured Identity Provider", "auth_method_strength", actionDocsIdentityProviders},
					{"8.3.4", "Login Attempt Limits", "Invalid login attempt lockout is managed by your Identity Provider", pseudoCheckIdpDelegated, actionExternalIdP},
					{"8.3.6", "Multi-Factor Authentication", "MFA enforcement is configured in your Identity Provider settings", "mfa_status", actionExternalIdP},
					{"8.6.1", "System Account Management", "Service accounts are managed with least privilege principles", "service_accounts_managed", actionAppServiceAccounts},
				},
			},
			{
				ID:    "Req10",
				Title: "Log and Monitor Access",
				Controls: []complianceControlDef{
					{"10.1", "Logging Processes", "Session recording is always enabled, capturing all access to system components", "session_recording", actionDocsSessionRecording},
					{"10.2.1", "Audit Logs Enabled", "Every session is automatically logged with complete audit trail information", "session_recording", actionDocsSessionRecording},
					{"10.2.1.1", "User Access Logging", "Individual user access to cardholder data is logged with email and resource role", "user_activity_logged", actionAppSessions},
					{"10.2.1.2", "Admin Action Logging", "All administrative and privileged actions are recorded in session logs", "admin_actions_logged", actionAppSessions},
					{"10.2.1.3", "Audit Log Access", "Access to audit logs themselves is not currently tracked", pseudoCheckGapNone, actionNone},
					{"10.2.1.4", "Failed Access Logging", "Failed and invalid access attempts are captured in session status tracking", "audit_log_details", actionAppSessions},
					{"10.2.1.5", "Credential Change Logging", "Authentication credential changes are logged by your Identity Provider", pseudoCheckIdpDelegated, actionExternalIdP},
					{"10.2.2", "Required Log Details", "Sessions capture user email, timestamp, resource role, and action type", "audit_log_details", actionDocsSessionRecording},
					{"10.3.1", "Log Capture Failures", "Agent health monitoring detects and alerts on logging infrastructure failures", "agent_health", actionAppAgents},
					{"10.4.1", "Daily Log Review", "Session review tools enable daily audit log review and investigation", "activity_monitoring", actionAppSessions},
					{"10.4.1.1", "Automated Log Review", "SIEM/Webhook integration enables automated audit log analysis and alerting", "automated_log_review", actionAppWebhooks},
					{"10.5.1", "12-Month Retention", "Audit log retention period depends on your deployment and storage configuration", "log_retention", actionExternalInfra},
					{"10.6.1", "Time Synchronization", "System time synchronization is enabled by default across all agents", "transmission_encryption", actionDocsArchitecture},
					{"10.7.1", "Security System Failures", "Agent health and SIEM integration detect and report critical system failures", "security_event_alerts", actionAppAgents},
				},
			},
		},
	},
	{
		ID:   "hipaa",
		Name: "HIPAA Security Rule",
		Groups: []complianceGroupDef{
			{
				ID:    "312a",
				Title: "§164.312(a) Access Control",
				Controls: []complianceControlDef{
					{"§312(a)(1)", "ePHI Access Controls", "Group-based access controls restrict who can access systems containing health information", "role_based_access", actionAppUsers},
					{"§312(a)(2)(i)", "Unique User ID", "SSO integration provides unique identification for every user accessing ePHI", "unique_user_ids", actionDocsIdentityProviders},
					{"§312(a)(2)(ii)", "Emergency Access", "Service accounts provide emergency access procedures when normal access is unavailable", "service_accounts_managed", actionAppServiceAccounts},
					{"§312(a)(2)(iii)", "Automatic Logoff", "Sessions have defined end times, preventing unauthorized access from idle sessions", "session_recording", actionDocsSessionRecording},
					{"§312(a)(2)(iv)", "Encryption", "AI Data Masking and TLS encryption protect ePHI at rest and in transit", "masking_enabled", actionAppResources},
				},
			},
			{
				ID:    "312b",
				Title: "§164.312(b) Audit Controls",
				Controls: []complianceControlDef{
					{"§312(b)", "Audit Controls", "Comprehensive session recording captures all activity in systems containing ePHI", "session_recording", actionDocsSessionRecording},
					{"§312(b).1", "Access Recording", "Every access to ePHI is logged with user identity, resource role, and timestamp", "user_activity_logged", actionAppSessions},
					{"§312(b).2", "Modification Recording", "All data modifications are captured in the session event stream for audit purposes", "audit_log_details", actionAppSessions},
					{"§312(b).3", "Activity Examination", "SIEM integration and session downloads enable examination of system activity", "automated_log_review", actionAppWebhooks},
				},
			},
			{
				ID:    "312c",
				Title: "§164.312(c) Integrity",
				Controls: []complianceControlDef{
					{"§312(c)(1)", "ePHI Integrity", "Recorded sessions are immutable, protecting audit trails from alteration or destruction", "session_integrity", actionDocsArchitecture},
					{"§312(c)(2)", "ePHI Authentication", "Session metadata and event tracking authenticate that ePHI has not been altered", "session_integrity", actionDocsSessionRecording},
				},
			},
			{
				ID:    "312d",
				Title: "§164.312(d) Person or Entity Authentication",
				Controls: []complianceControlDef{
					{"§312(d)", "Identity Verification", "Identity Provider integration verifies user identity before granting ePHI access", "sso_enabled", actionDocsIdentityProviders},
					{"§312(d).1", "Authentication Methods", "SSO/OIDC/SAML authentication methods verify persons requesting ePHI access", "auth_method_strength", actionDocsIdentityProviders},
				},
			},
			{
				ID:    "312e",
				Title: "§164.312(e) Transmission Security",
				Controls: []complianceControlDef{
					{"§312(e)(1)", "Transmission Guard", "TLS-encrypted gRPC tunnels guard against unauthorized access during transmission", "secure_tunnel", actionDocsArchitecture},
					{"§312(e)(2)(i)", "Integrity Controls", "Secure tunnel architecture ensures data integrity during transmission", "transmission_encryption", actionDocsArchitecture},
					{"§312(e)(2)(ii)", "Transmission Encryption", "All ePHI transmissions are encrypted using TLS on agent connections", "transmission_encryption", actionDocsArchitecture},
				},
			},
			{
				ID:    "308",
				Title: "§164.308 Administrative Safeguards",
				Controls: []complianceControlDef{
					{"§308(a)(1)(ii)(D)", "Activity Review", "Session monitoring and SIEM integration enable regular information system activity review", "activity_monitoring", actionAppWebhooks},
					{"§308(a)(3)", "Workforce Security", "User management tracks workforce members with access to ePHI", "user_access_reviews", actionAppUsers},
					{"§308(a)(4)", "Access Management", "Role-based access via user groups manages information access authorizations", "least_privilege", actionAppResources},
					{"§308(a)(5)(ii)(C)", "Login Monitoring", "All user logins are recorded in session tracking for security awareness", "user_activity_logged", actionAppSessions},
				},
			},
		},
	},
	{
		ID:   "best_practices",
		Name: "Hoop Best Practices",
		Groups: []complianceGroupDef{
			{
				ID:    "BP-AG",
				Title: "Access Governance",
				Controls: []complianceControlDef{
					{"BP-AG-01", "Least Privilege Groups", "At least 3 user groups are configured to enforce granular least privilege access", "rbac_groups", actionAppUsers},
					{"BP-AG-02", "No Shared Credentials", "SSO integration eliminates shared credentials and ensures individual accountability", "sso_enabled", actionDocsIdentityProviders},
					{"BP-AG-03", "Production JIT Access", "Production resource roles require just-in-time approval before access is granted", "jit_reviews", actionAppResources},
					{"BP-AG-04", "Service Account Inventory", "Service accounts are documented and available for privileged access review", "service_accounts_managed", actionAppServiceAccounts},
				},
			},
			{
				ID:    "BP-DP",
				Title: "Data Protection",
				Controls: []complianceControlDef{
					{"BP-DP-01", "Database Masking", "AI Data Masking is enabled on all database resource roles to protect sensitive data", "masking_coverage", actionAppResources},
					{"BP-DP-02", "High-Risk PII Types", "Core sensitive data types (EMAIL, SSN, credit card) are configured for detection", "sensitive_types_configured", actionDocsDataMasking},
					{"BP-DP-03", "Production Guardrails", "Guardrail rules are applied to filter dangerous commands on production resources", "guardrails_active", actionAppGuardrails},
					{"BP-DP-04", "DLP Provider", "Data Loss Prevention provider is configured and operational", pseudoCheckInfraDelegated, actionExternalInfra},
				},
			},
			{
				ID:    "BP-OS",
				Title: "Operational Security",
				Controls: []complianceControlDef{
					{"BP-OS-01", "Agent Connectivity", "All deployed agents are connected and operational", "agents_online", actionAppAgents},
					{"BP-OS-02", "Agent Versions", "All agents should be running the latest supported version", "agent_version_current", actionDocsAgent},
					{"BP-OS-03", "SIEM Integration", "Webhook integration is configured to forward security events to your SIEM", "siem_integration", actionAppWebhooks},
					{"BP-OS-04", "Session Export", "Sessions can be exported for long-term retention and compliance archival", "session_recording", actionDocsSessionRecording},
				},
			},
			{
				ID:    "BP-MR",
				Title: "Monitoring & Response",
				Controls: []complianceControlDef{
					{"BP-MR-01", "Active Monitoring", "Sessions are being recorded, indicating active usage monitoring", "activity_monitoring", actionAppSessions},
					{"BP-MR-02", "Review Response SLA", "Pending access reviews are responded to within acceptable timeframes", "review_response_sla", actionAppReviews},
					{"BP-MR-03", "Data Discovery", "Sensitive data types detected during sessions are tracked for discovery purposes", "activity_monitoring", actionAppSessions},
				},
			},
		},
	},
}
