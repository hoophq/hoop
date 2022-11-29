package proto

import "time"

const (
	PacketKeepAliveType          PacketType = "KeepAlive"
	PacketCloseTCPConnectionType PacketType = "CloseTCPConnection"
	PacketAgentGatewayConnectOK  PacketType = "Agent::Gateway::ConnectOK"

	// client->agent connection
	PacketClientGatewayConnectType  PacketType = "Client::Gateway::Connect"
	PacketClientAgentConnectType    PacketType = "Client::Agent::Connect"
	PacketClientAgentConnectOKType  PacketType = "Client::Agent::ConnectOK"
	PacketClientAgentConnectErrType PacketType = "Client::Agent::ConnectErr"

	// client->agent exec
	PacketClientGatewayExecType        PacketType = "Client::Gateway::Exec"
	PacketClientGatewayExecWaitType    PacketType = "Client::Gateway::ExecWait"
	PacketClientGatewayExecApproveType PacketType = "Client::Gateway::ExecApprove"
	PacketClientGatewayExecRejectType  PacketType = "Client::Gateway::ExecReject"
	PacketClientExecAgentOfflineType   PacketType = "Client::Gateway::ExecAgentOffline"
	PacketClientAgentExecType          PacketType = "Client::Agent::Exec"
	PacketClientAgentExecOKType        PacketType = "Client::Agent::ExecOK"
	PacketClientAgentExecErrType       PacketType = "Client::Agent::ExecErr"

	// terminal messages
	PacketTerminalClientWriteStdoutType PacketType = "Terminal::Client::WriteStdout"
	PacketTerminalWriteAgentStdinType   PacketType = "Terminal::Agent::WriteStdin"
	PacketTerminalCloseType             PacketType = "Terminal::Close"

	// Raw TCP
	PacketTCPWriteServerType PacketType = "TCP::WriteServer"
	PacketTCPWriteClientType PacketType = "TCP::WriteClient"

	// PG protocol messages
	PacketPGWriteServerType PacketType = "PG::WriteServer"
	PacketPGWriteClientType PacketType = "PG::WriteClient"

	SpecGatewaySessionID          string = "gateway.session_id"
	SpecConnectionType            string = "gateway.connection_type"
	SpecDLPTransformationSummary  string = "dlp.transformation_summary"
	SpecClientConnectionID        string = "client.connection_id"
	SpecClientExecExitCodeKey     string = "terminal.exit_code"
	SpecClientExecArgsKey         string = "terminal.args"
	SpecAgentConnectionParamsKey  string = "agent.connection_params"
	SpecAgentGCPRawCredentialsKey string = "agent.gcp_credentials"
	SpecTCPServerConnectKey       string = "tcp.server_connect"
	SpecReviewDataKey             string = "review.data"
	SpecGatewayReviewID           string = "review.id"

	DefaultKeepAlive time.Duration = 10 * time.Second

	ConnectionTypeCommandLine string = "command-line"
	ConnectionTypePostgres    string = "postgres"
	ConnectionTypeTCP         string = "tcp"

	DevProfile = "dev"

	ProviderOkta = "okta"

	ConnectionOriginAgent  = "agent"
	ConnectionOriginClient = "client"

	ClientLoginCallbackAddress string = "127.0.0.1:3587"

	ClientVerbConnect = "connect"
	ClientVerbExec    = "exec"
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
