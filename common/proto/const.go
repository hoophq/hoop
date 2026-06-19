package proto

import "time"

type ConnectionType string

func (c ConnectionType) String() string { return string(c) }
func (c ConnectionType) Bytes() []byte  { return []byte(c) }

const (
	SpecGatewaySessionID         string = "gateway.session_id"
	SpecConnectionName           string = "gateway.connection_name"
	SpecConnectionType           string = "gateway.connection_type"
	SpecConnectionCommand        string = "gateway.connection_command"
	SpecHasReviewKey             string = "gateway.has_review"
	SpecPluginDcmDataKey         string = "plugin.dcm_data"
	SpecDLPTransformationSummary string = "dlp.transformation_summary" // Deprecated: see spectypes.DataMaskingInfoKey
	SpecClientConnectionID       string = "client.connection_id"
	SpecClientExitCodeKey        string = "client.exit_code"
	SpecClientGuardRailsInfoKey  string = "client.guardrails_info"
	SpecClientRequestPort        string = "client.request_port"
	SpecClientSSHHostKey         string = "client.ssh_host_key"
	SpecClientExecCommandKey     string = "client.command"
	SpecClientExecArgsKey        string = "terminal.args"
	SpecClientExecEnvVar         string = "terminal.envvars"
	SpecAgentVersion             string = "agent.version"
	SpecAgentConnectionParamsKey string = "agent.connection_params"
	SpecAwsSSMWebsocketMsgType   string = "aws.websocket.message_type"
	SpecAwsSSMEc2InstanceId      string = "aws.ssm.ec2.instance_id"
	SpecHttpProxyBaseUrl         string = "httpproxy.base_url"
	SpecHttpProxyRequestIDs      string = "httpproxy.request_id"
	// SpecHttpProxyServerKey marks HttpProxyConnectionWrite packets emitted by
	// the gateway HTTP proxy server. That path frames and analyzes each request
	// itself (gateway/proxyproto/httpproxy), so the transport-level connect-path
	// analyzer uses this marker to skip them and avoid double analysis.
	SpecHttpProxyServerKey string = "httpproxy.server"

	// DEPRECATED: spec items deprecated
	SpecAgentDlpProvider             string = "agent.dlp_provider"
	SpecAgentMSPresidioAnalyzerURL   string = "agent.mspresidio_analyzer_url"
	SpecAgentMSPresidioAnonymizerURL string = "agent.mspresidio_anonymizer_url"
	SpecAgentGCPRawCredentialsKey    string = "agent.gcp_credentials"

	SpecFeatureFlagsKey     string = "feature-flags"
	SpecTCPServerConnectKey string = "tcp.server_connect"
	SpecReviewDataKey       string = "review.data"
	SpecGatewayReviewID     string = "review.id"
	SpecGatewayJitID        string = "jit.id"
	SpecJitStatus           string = "jit.status"
	SpecJitTimeout          string = "jit.timeout"

	DefaultKeepAlive time.Duration = 10 * time.Second

	ConnectionTypeCommandLine ConnectionType = "command-line"
	ConnectionTypeClaudeCode  ConnectionType = "claude-code"
	ConnectionTypeDynamoDB    ConnectionType = "dynamodb"
	ConnectionTypeCloudWatch  ConnectionType = "cloudwatch"
	ConnectionTypePostgres    ConnectionType = "postgres"
	ConnectionTypeMySQL       ConnectionType = "mysql"
	ConnectionTypeMSSQL       ConnectionType = "mssql"
	ConnectionTypeMongoDB     ConnectionType = "mongodb"
	ConnectionTypeOracleDB    ConnectionType = "oracledb"
	ConnectionTypeTCP         ConnectionType = "tcp"
	ConnectionTypeHttpProxy   ConnectionType = "httpproxy"
	ConnectionTypeKubernetes  ConnectionType = "kubernetes"
	ConnectionTypeSSH         ConnectionType = "ssh"
	ConnectionTypeRDP         ConnectionType = "rdp"
	ConnectionTypeSSM         ConnectionType = "aws-ssm"

	ConnectionOriginAgent              = "agent"
	ConnectionOriginClient             = "client"
	ConnectionOriginClientProxyManager = "client-proxymanager"
	ConnectionOriginClientAPI          = "client-api"
	ConnectionOriginClientAPIRunbooks  = "client-api-runbooks"

	SystemAgentEnvs = "system.agent.envs"

	ClientLoginCallbackAddress string = "127.0.0.1:3587"

	ClientVerbConnect   = "connect"
	ClientVerbExec      = "exec"
	ClientVerbPlainExec = "plain-exec"

	// SessionOrigin* are the product-level origins of a session. They are
	// persisted on the session record (sessions.origin) and emitted on the
	// session analytics events so we can tell how a session was initiated.
	// Unlike ConnectionOrigin* (a transport-level concept), these distinguish
	// product surfaces that share the same transport origin (e.g. MCP and the
	// REST API both connect with ConnectionOriginClientAPI).
	SessionOriginCLI          = "cli"
	SessionOriginWebApp       = "webapp"
	SessionOriginAPI          = "api"
	SessionOriginMCP          = "mcp"
	SessionOriginRunbooks     = "runbooks"
	SessionOriginProxyManager = "proxymanager"
	SessionOriginAgent        = "agent"
	SessionOriginUnknown      = "unknown"

	SessionPhaseClientConnect       = "client-connect"
	SessionPhaseClientConnected     = "client-connected"
	SessionPhaseClientSessionOpen   = "client-session-open"
	SessionPhaseClientSessionClose  = "client-session-close"
	SessionPhaseGatewaySessionClose = "gateway-session-close"
	SessionPhaseClientErr           = "client-err"

	CustomClaimGroups = "https://app.hoop.dev/groups"
	DefaultOrgName    = "default"

	AgentModeEmbeddedType        string = "embedded"
	AgentModeStandardType        string = "standard"
	AgentModeMultiConnectionType string = "multi-connection"

	PreConnectStatusConnectType string = "CONNECT"
	PreConnectStatusBackoffType string = "BACKOFF"
)

// SessionOriginFromClientOrigin maps a transport-level client origin to the
// product-level session origin. It is used by the gRPC session-creation path,
// which only sees the transport origin. API-driven surfaces (REST, MCP,
// runbooks) declare their SessionOrigin explicitly at the call site instead.
func SessionOriginFromClientOrigin(clientOrigin string) string {
	switch clientOrigin {
	case ConnectionOriginClient:
		return SessionOriginCLI
	case ConnectionOriginClientProxyManager:
		return SessionOriginProxyManager
	case ConnectionOriginClientAPI:
		return SessionOriginAPI
	case ConnectionOriginClientAPIRunbooks:
		return SessionOriginRunbooks
	case ConnectionOriginAgent:
		return SessionOriginAgent
	default:
		return SessionOriginUnknown
	}
}

// SessionOriginFromUserAgent maps a normalized user-agent token (as produced by
// apiutils.NormalizeUserAgent — i.e. the leading "product" of the User-Client
// or User-Agent header) to the product-level session origin. It is used by HTTP
// entry points that only know the caller through that header, such as the
// connection-credentials mint and the REST exec endpoint. The webapp sends
// "webapp.core" and the CLI sends "hoopcli"; anything else is treated as a raw
// API consumer.
func SessionOriginFromUserAgent(normalizedUserAgent string) string {
	switch normalizedUserAgent {
	case "webapp.core":
		return SessionOriginWebApp
	case "hoopcli":
		return SessionOriginCLI
	default:
		return SessionOriginAPI
	}
}

var DefaultInfoTypes = []string{
	"PHONE_NUMBER",
	"CREDIT_CARD_NUMBER",
	"CREDIT_CARD_TRACK_NUMBER",
	"EMAIL_ADDRESS",
	"IBAN_CODE",
	"HTTP_COOKIE",
	"IMEI_HARDWARE_ID",
	"IP_ADDRESS",
	"STORAGE_SIGNED_URL",
	"URL",
	"VEHICLE_IDENTIFICATION_NUMBER",
	"BRAZIL_CPF_NUMBER",
	"AMERICAN_BANKERS_CUSIP_ID",
	"FDA_CODE",
	"US_PASSPORT",
	"US_SOCIAL_SECURITY_NUMBER",
}
