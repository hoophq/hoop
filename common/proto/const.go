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
	SpecClientRequestPort        string = "client.request_port"
	SpecClientSSHHostKey         string = "client.ssh_host_key"
	SpecClientExecCommandKey     string = "client.command"
	SpecClientExecArgsKey        string = "terminal.args"
	SpecClientExecEnvVar         string = "terminal.envvars"
	SpecAgentConnectionParamsKey string = "agent.connection_params"

	// DEPRECATED: spec items deprecated
	SpecAgentDlpProvider             string = "agent.dlp_provider"
	SpecAgentMSPresidioAnalyzerURL   string = "agent.mspresidio_analyzer_url"
	SpecAgentMSPresidioAnonymizerURL string = "agent.mspresidio_anonymizer_url"
	SpecAgentGCPRawCredentialsKey    string = "agent.gcp_credentials"

	SpecTCPServerConnectKey string = "tcp.server_connect"
	SpecReviewDataKey       string = "review.data"
	SpecGatewayReviewID     string = "review.id"
	SpecGatewayJitID        string = "jit.id"
	SpecJitStatus           string = "jit.status"
	SpecJitTimeout          string = "jit.timeout"

	DefaultKeepAlive time.Duration = 10 * time.Second

	ConnectionTypeCommandLine ConnectionType = "command-line"
	ConnectionTypeDynamoDB    ConnectionType = "dynamodb"
	ConnectionTypeCloudWatch  ConnectionType = "cloudwatch"
	ConnectionTypePostgres    ConnectionType = "postgres"
	ConnectionTypeMySQL       ConnectionType = "mysql"
	ConnectionTypeMSSQL       ConnectionType = "mssql"
	ConnectionTypeMongoDB     ConnectionType = "mongodb"
	ConnectionTypeOracleDB    ConnectionType = "oracledb"
	ConnectionTypeTCP         ConnectionType = "tcp"
	ConnectionTypeHttpProxy   ConnectionType = "httpproxy"
	ConnectionTypeSSH         ConnectionType = "ssh"
	ConnectionTypeRDP         ConnectionType = "rdp"

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
