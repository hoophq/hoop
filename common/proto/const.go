package proto

import "time"

const (
	SpecGatewaySessionID          string = "gateway.session_id"
	SpecConnectionType            string = "gateway.connection_type"
	SpecDLPTransformationSummary  string = "dlp.transformation_summary"
	SpecClientConnectionID        string = "client.connection_id"
	SpecClientExitCodeKey         string = "client.exit_code"
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

	ConnectionTypeCommandLine string = "command-line"
	ConnectionTypePostgres    string = "postgres"
	ConnectionTypeTCP         string = "tcp"

	DevProfile = "dev"

	ProviderOkta = "okta"

	ConnectionOriginAgent     = "agent"
	ConnectionOriginClient    = "client"
	ConnectionOriginClientAPI = "client-api"

	ClientLoginCallbackAddress string = "127.0.0.1:3587"

	ClientVerbConnect = "connect"
	ClientVerbExec    = "exec"

	SessionPhaseClientConnect       = "client-connect"
	SessionPhaseClientConnected     = "client-connected"
	SessionPhaseClientSessionOpen   = "client-session-open"
	SessionPhaseClientSessionClose  = "client-session-close"
	SessionPhaseGatewaySessionClose = "gateway-session-close"
	SessionPhaseClientErr           = "client-err"
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
