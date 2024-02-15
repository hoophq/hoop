package proto

import "time"

type ConnectionType string

func (c ConnectionType) String() string { return string(c) }
func (c ConnectionType) Bytes() []byte  { return []byte(c) }

const (
	SpecGatewaySessionID          string = "gateway.session_id"
	SpecConnectionName            string = "gateway.connection_name"
	SpecConnectionType            string = "gateway.connection_type"
	SpecPluginDcmDataKey          string = "plugin.dcm_data"
	SpecDLPTransformationSummary  string = "dlp.transformation_summary"
	SpecClientConnectionID        string = "client.connection_id"
	SpecClientExitCodeKey         string = "client.exit_code"
	SpecClientRequestPort         string = "client.request_port"
	SpecClientExecArgsKey         string = "terminal.args"
	SpecClientExecEnvVar          string = "terminal.envvars"
	SpecAgentConnectionParamsKey  string = "agent.connection_params"
	SpecAgentGCPRawCredentialsKey string = "agent.gcp_credentials"
	SpecTCPServerConnectKey       string = "tcp.server_connect"
	SpecReviewDataKey             string = "review.data"
	SpecGatewayReviewID           string = "review.id"
	SpecGatewayJitID              string = "jit.id"
	SpecJitStatus                 string = "jit.status"
	SpecJitTimeout                string = "jit.timeout"

	DefaultKeepAlive time.Duration = 10 * time.Second

	ConnectionTypeCommandLine ConnectionType = "command-line"
	ConnectionTypePostgres    ConnectionType = "postgres"
	ConnectionTypeMySQL       ConnectionType = "mysql"
	ConnectionTypeMSSQL       ConnectionType = "mssql"
	ConnectionTypeTCP         ConnectionType = "tcp"

	ConnectionOriginAgent              = "agent"
	ConnectionOriginClient             = "client"
	ConnectionOriginClientProxyManager = "client-proxymanager"
	ConnectionOriginClientAPI          = "client-api"

	SystemAgentEnvs = "system.agent.envs"

	ClientLoginCallbackAddress string = "127.0.0.1:3587"

	ClientVerbConnect = "connect"
	ClientVerbExec    = "exec"

	SessionPhaseClientConnect       = "client-connect"
	SessionPhaseClientConnected     = "client-connected"
	SessionPhaseClientSessionOpen   = "client-session-open"
	SessionPhaseClientSessionClose  = "client-session-close"
	SessionPhaseGatewaySessionClose = "gateway-session-close"
	SessionPhaseClientErr           = "client-err"

	CustomClaimOrg    = "https://app.hoop.dev/org"
	CustomClaimGroups = "https://app.hoop.dev/groups"
	DefaultOrgName    = "default"

	AgentModeEmbeddedType string = "embedded"
	AgentModeStandardType string = "standard"
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
