package services

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/httpproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/postgresproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/ssmproxy"
)

const credentialKeySize = 32

const noExpirySentinel = "9999-12-31T00:00:00Z"

func GenerateSecretKey(connType proto.ConnectionType) (plaintext string, hash string, err error) {
	switch connType {
	case proto.ConnectionTypePostgres:
		return keys.GenerateSecureRandomKey("pg", credentialKeySize)
	case proto.ConnectionTypeSSH:
		return keys.GenerateSecureRandomKey("ssh", credentialKeySize)
	case proto.ConnectionTypeRDP:
		return keys.GenerateSecureRandomKey("rdp", credentialKeySize)
	case proto.ConnectionTypeSSM:
		return keys.GenerateSecureRandomKey("aws-ssm", credentialKeySize)
	case proto.ConnectionTypeHttpProxy:
		return keys.GenerateSecureRandomKey("httpproxy", credentialKeySize)
	case proto.ConnectionTypeClaudeCode:
		return keys.GenerateSecureRandomKey("claude-code", credentialKeySize)
	case proto.ConnectionTypeKubernetes:
		return keys.GenerateSecureRandomKey("k8s", credentialKeySize)
	default:
		return "", "", fmt.Errorf("unsupported connection type %v", connType)
	}
}

type CredentialInfo struct {
	ID                string
	ConnectionName    string
	ConnectionType    string
	ConnectionSubType string
	SecretKey         string
	Hostname          string
	Port              string
	Postgres          *PostgresCredentialInfo
	SSH               *SSHCredentialInfo
	RDP               *RDPCredentialInfo
	SSM               *SSMCredentialInfo
	HTTPProxy         *HTTPProxyCredentialInfo
}

type PostgresCredentialInfo struct {
	DatabaseName     string
	ConnectionString string
}

type SSHCredentialInfo struct {
	Command string
}

type RDPCredentialInfo struct {
	Command string
}

type SSMCredentialInfo struct {
	EndpointURL        string
	AwsAccessKeyId     string
	AwsSecretAccessKey string
	ConnectionString   string
}

type HTTPProxyCredentialInfo struct {
	ProxyToken string
	Command    string
}

func BuildCredentialInfo(
	cred *models.ConnectionCredentials,
	conn *models.Connection,
	serverConf *models.ServerMiscConfig,
	secretKey string,
) *CredentialInfo {
	const dummyString = "hoop"

	info := &CredentialInfo{
		ID:                cred.ID,
		ConnectionName:    cred.ConnectionName,
		ConnectionType:    conn.Type,
		ConnectionSubType: conn.SubType.String,
		SecretKey:         secretKey,
	}

	connectionType := toConnectionType(cred.ConnectionType, conn.SubType.String)
	serverHost, serverPort := getServerHostAndPort(serverConf, connectionType)
	info.Hostname = serverHost
	info.Port = serverPort

	switch connectionType {
	case proto.ConnectionTypePostgres:
		var databaseName string
		defaultDBEnc := conn.Envs["envvar:DB"]
		if defaultDBEnc != "" {
			defaultDBBytes, _ := base64.StdEncoding.DecodeString(defaultDBEnc)
			databaseName = string(defaultDBBytes)
		}
		if databaseName == "" {
			databaseName = "postgres"
		}
		sslMode := "disable"
		if appconfig.Get().GatewayTLSKey() != "" {
			sslMode = "require"
		}
		info.Postgres = &PostgresCredentialInfo{
			DatabaseName: databaseName,
			ConnectionString: fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
				secretKey, dummyString, serverHost, serverPort, databaseName, sslMode),
		}
	case proto.ConnectionTypeSSH:
		info.SSH = &SSHCredentialInfo{
			Command: fmt.Sprintf("sshpass -p '%s' ssh %s@%s -p %s", dummyString, secretKey, serverHost, serverPort),
		}
	case proto.ConnectionTypeRDP:
		info.RDP = &RDPCredentialInfo{
			Command: fmt.Sprintf("xfreerdp /v:%s:%s /u:%s /p:%s", serverHost, serverPort, secretKey, secretKey),
		}
	case proto.ConnectionTypeSSM:
		accessKeyId, err := ssmproxy.UUIDToAccessKey(cred.ID)
		if err != nil {
			return info
		}
		if len(cred.SecretKeyHash) < 40 {
			return info
		}
		endpoint := fmt.Sprintf("%s/ssm/", appconfig.Get().ApiURL())
		accessSecret := cred.SecretKeyHash[:40]
		info.SSM = &SSMCredentialInfo{
			EndpointURL:        endpoint,
			AwsAccessKeyId:     accessKeyId,
			AwsSecretAccessKey: accessSecret,
			ConnectionString: fmt.Sprintf(
				"AWS_ACCESS_KEY_ID=%q AWS_SECRET_ACCESS_KEY=%q aws ssm start-session --target {TARGET_INSTANCE} --endpoint-url %q",
				accessKeyId, accessSecret, endpoint),
		}
	case proto.ConnectionTypeHttpProxy, proto.ConnectionTypeKubernetes:
		scheme := "http"
		host := serverHost
		if appconfig.Get().GatewayTLSKey() != "" {
			scheme = "https"
			if apiURL, err := url.Parse(appconfig.Get().ApiURL()); err == nil && apiURL.Hostname() != "" {
				host = apiURL.Hostname()
			}
		}
		baseCommand := fmt.Sprintf("%s://%s:%s/", scheme, host, serverPort)
		curlCommand := fmt.Sprintf("curl -H 'Authorization: %s' %s", secretKey, baseCommand)
		browserCommand := fmt.Sprintf("%s%s", baseCommand, secretKey)
		jsonCommandsString := `{
				"curl": "` + curlCommand + `",
				"browser": "` + browserCommand + `"
			}`
		info.ConnectionType = connectionType.String()
		info.HTTPProxy = &HTTPProxyCredentialInfo{
			ProxyToken: secretKey,
			Command:    jsonCommandsString,
		}
	}

	return info
}

func toConnectionType(connectionType, subtype string) proto.ConnectionType {
	switch connectionType {
	case "command-line":
		switch subtype {
		case "kubernetes", "kubernetes-eks":
			return proto.ConnectionType(proto.ConnectionTypeKubernetes)
		case "kubernetes-token", "httpproxy":
			return proto.ConnectionType(proto.ConnectionTypeHttpProxy)
		}
	}
	return proto.ConnectionType(connectionType)
}

func MapValidSubtypeToHttpProxy(conn *models.Connection) proto.ConnectionType {
	switch conn.SubType.String {
	case "grafana", "kibana", "kubernetes-token":
		return proto.ConnectionTypeHttpProxy
	case "git", "github":
		return proto.ConnectionTypeSSH
	case "kubernetes", "kubernetes-eks":
		return proto.ConnectionTypeKubernetes
	default:
		return proto.ConnectionType(conn.SubType.String)
	}
}

func getServerHostAndPort(serverConf *models.ServerMiscConfig, connType proto.ConnectionType) (host, portNumber string) {
	var listenAddr string
	switch connType {
	case proto.ConnectionTypePostgres:
		if serverConf != nil && serverConf.PostgresServerConfig != nil {
			listenAddr = serverConf.PostgresServerConfig.ListenAddress
		}
	case proto.ConnectionTypeSSH:
		if serverConf != nil && serverConf.SSHServerConfig != nil {
			listenAddr = serverConf.SSHServerConfig.ListenAddress
		}
	case proto.ConnectionTypeRDP:
		if serverConf != nil && serverConf.RDPServerConfig != nil {
			listenAddr = serverConf.RDPServerConfig.ListenAddress
		}
	case proto.ConnectionTypeHttpProxy, proto.ConnectionTypeKubernetes:
		if serverConf != nil && serverConf.HttpProxyServerConfig != nil {
			listenAddr = serverConf.HttpProxyServerConfig.ListenAddress
		}
	}
	host, portNumber, _ = strings.Cut(listenAddr, ":")
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return
}

func IsConnectionTypeConfigured(serverConf *models.ServerMiscConfig, connType proto.ConnectionType) bool {
	if connType == proto.ConnectionTypeSSM {
		return true
	}
	if serverConf == nil {
		return false
	}
	switch connType {
	case proto.ConnectionTypePostgres:
		return serverConf.PostgresServerConfig != nil && serverConf.PostgresServerConfig.ListenAddress != ""
	case proto.ConnectionTypeSSH:
		return serverConf.SSHServerConfig != nil && serverConf.SSHServerConfig.ListenAddress != ""
	case proto.ConnectionTypeRDP:
		return serverConf.RDPServerConfig != nil && serverConf.RDPServerConfig.ListenAddress != ""
	case proto.ConnectionTypeHttpProxy, proto.ConnectionTypeKubernetes, proto.ConnectionTypeClaudeCode:
		return serverConf.HttpProxyServerConfig != nil && serverConf.HttpProxyServerConfig.ListenAddress != ""
	default:
		return false
	}
}

func ProvisionCredentialForConnection(mi *models.MachineIdentity, connName string, conn *models.Connection, serverConf *models.ServerMiscConfig) (*models.ConnectionCredentials, string, *CredentialInfo, error) {
	subtype := MapValidSubtypeToHttpProxy(conn)
	connType := proto.ConnectionType(subtype.String())

	if !IsConnectionTypeConfigured(serverConf, connType) {
		return nil, "", nil, fmt.Errorf("listening address is not configured for connection type %s", connType)
	}

	secretKey, secretKeyHash, err := GenerateSecretKey(connType)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed generating secret key for connection %s: %w", connName, err)
	}

	sid := uuid.NewString()
	newSession := models.Session{
		ID:                sid,
		OrgID:             mi.OrgID,
		UserEmail:         "",
		UserID:            mi.ID,
		UserName:          mi.Name,
		Connection:        conn.Name,
		ConnectionType:    conn.Type,
		ConnectionSubtype: conn.SubType.String,
		ConnectionTags:    conn.ConnectionTags,
		Verb:              proto.ClientVerbConnect,
		Status:            string(openapi.SessionStatusOpen),
		CreatedAt:         time.Now().UTC(),
		IdentityType:      "machine",
	}
	// Persist session
	if err := models.UpsertSession(newSession); err != nil {
		log.Errorf("failed creating session, err=%v", err)
		return nil, "", nil, fmt.Errorf("failed creating session for connection %s: %w", connName, err)
	}

	noExpiry, _ := time.Parse(time.RFC3339, noExpirySentinel)
	cred := &models.ConnectionCredentials{
		ID:             uuid.NewString(),
		OrgID:          mi.OrgID,
		UserSubject:    mi.ID,
		ConnectionName: conn.Name,
		ConnectionType: proto.ToConnectionType(conn.Type, conn.SubType.String).String(),
		SecretKeyHash:  secretKeyHash,
		SessionID:      sid,
		CreatedAt:      time.Now().UTC(),
		ExpireAt:       noExpiry,
	}
	dbCred, err := models.CreateConnectionCredentials(cred)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed creating connection credential: %w", err)
	}

	mic := &models.MachineIdentityCredential{
		ID:                     uuid.NewString(),
		OrgID:                  mi.OrgID,
		MachineIdentityID:      mi.ID,
		ConnectionCredentialID: dbCred.ID,
		ConnectionName:         connName,
		SecretKey:              secretKey,
		CreatedAt:              time.Now().UTC(),
	}
	if err := models.CreateMachineIdentityCredential(mic); err != nil {
		return nil, "", nil, fmt.Errorf("failed creating machine identity credential: %w", err)
	}

	connCopy := *conn
	connCopy.SubType = sql.NullString{String: subtype.String(), Valid: true}
	info := BuildCredentialInfo(dbCred, &connCopy, serverConf, secretKey)

	return dbCred, secretKey, info, nil
}

// revokeActiveProxySessions cancels any in-flight proxy sessions for a revoked credential.
// This mirrors the proxy cancellation logic in the RevokeConnectionCredentials API handler.
func revokeActiveProxySessions(info *models.RevokedCredentialInfo) {
	if info == nil {
		return
	}
	connType := proto.ConnectionType(info.ConnectionType)
	switch connType {
	case proto.ConnectionTypePostgres:
		postgresproxy.GetServerInstance().RevokeByCredentialID(info.CredentialID)
	case proto.ConnectionTypeSSH:
		sshproxy.GetServerInstance().RevokeByCredentialID(info.CredentialID)
	case proto.ConnectionTypeRDP:
		broker.RevokeByCredentialID(info.CredentialID)
	case proto.ConnectionTypeHttpProxy, proto.ConnectionTypeKubernetes,
		proto.ConnectionTypeClaudeCode, proto.ConnectionTypeCommandLine:
		httpproxy.GetServerInstance().RevokeBySecretKeyHash(info.SecretKeyHash)
	case proto.ConnectionTypeSSM:
		// SSM has no persistent session store; DB invalidation blocks new connections
	default:
		log.Warnf("unknown connection type %s for proxy revocation of credential %s", info.ConnectionType, info.CredentialID)
	}
}
