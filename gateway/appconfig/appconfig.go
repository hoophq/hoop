package appconfig

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/envloader"

	idptypes "github.com/hoophq/hoop/gateway/idp/types"
)

// TODO: it should include all runtime configuration

const defaultWebappStaticUiPath string = "/app/ui/public"

type pgCredentials struct {
	connectionString string
	username         string
}
type Config struct {
	apiKey                          string
	askAICredentials                *url.URL
	authMethod                      idptypes.ProviderType
	pgCred                          *pgCredentials
	gcpDLPJsonCredentials           string
	forceUrlTokenExchange           bool
	dlpProvider                     string
	dlpMode                         string
	hasRedactCredentials            bool
	msPresidioAnalyzerURL           string
	msPresidioAnonymizerURL         string
	webhookAppKey                   string
	webhookAppURL                   *url.URL
	licenseSigningKey               *rsa.PrivateKey
	licenseSignerOrgID              string
	migrationPathFiles              string
	orgMultitenant                  bool
	analyticsTracking               bool
	apiURL                          string
	grpcURL                         string
	apiHostname                     string
	apiHost                         string
	apiScheme                       string
	apiURLPath                      string
	webappUsersManagement           string
	webappStaticUIPath              string
	disableSessionsDownload         bool
	disableClipboardCopyCut         bool
	gatewayUseTLS                   bool
	grpcClientTLSCa                 string
	gatewayTLSKey                   string
	gatewayTLSCert                  string
	gatewayAllowPlainText           bool
	gatewaySkipTLSVerify            bool
	sshClientHostKey                string
	integrationAWSInstanceRoleAllow bool

	spiffeMode          string
	spiffeBundleURL     string
	spiffeBundleFile    string
	spiffeBundleJWKS    string
	spiffeTrustDomain   string
	spiffeAudience      string
	spiffeRefreshPeriod time.Duration

	isLoaded bool
}

var runtimeConfig Config

// Load validate for any errors and set the RuntimeConfig var
func Load() error {
	if runtimeConfig.isLoaded {
		return nil
	}
	apiURL := os.Getenv("API_URL")
	if apiURL == "" {
		apiURL = "http://127.0.0.1:8009"
	}
	grpcURL := os.Getenv("GRPC_URL")
	if grpcURL == "" {
		grpcURL = "grpc://127.0.0.1:8010"
	}

	apiURL = strings.TrimSuffix(apiURL, "/")
	grpcURL = strings.TrimSuffix(grpcURL, "/")

	apiRawURL, err := url.Parse(apiURL)
	if err != nil {
		return fmt.Errorf("failed parsing API_URL env, reason=%v", err)
	}
	askAICred, err := loadAskAICredentials()
	if err != nil {
		return err
	}
	pgCred, err := loadPostgresCredentials()
	if err != nil {
		return err
	}
	migrationPathFiles := strings.TrimSuffix(os.Getenv("MIGRATION_PATH_FILES"), "/")
	if migrationPathFiles == "" {
		migrationPathFiles = "/app/migrations"
	}
	firstMigrationFilePath := fmt.Sprintf("%s/000001_init.up.sql", migrationPathFiles)
	if _, err := os.Stat(firstMigrationFilePath); err != nil {
		return fmt.Errorf("unable to find first migration file %v, err=%v", firstMigrationFilePath, err)
	}
	allowedOrgID, licensePrivKey, err := loadLicensePrivateKey()
	if err != nil {
		return err
	}
	gcpJsonCred, err := loadGcpDLPCredentials()
	if err != nil {
		return err
	}
	webappUsersManagement := os.Getenv("WEBAPP_USERS_MANAGEMENT")
	if webappUsersManagement == "" {
		webappUsersManagement = "on"
	}
	authMethod, err := loadAuthMethod()
	if err != nil {
		return err
	}
	webappStaticUiPath := os.Getenv("STATIC_UI_PATH")
	if webappStaticUiPath == "" {
		webappStaticUiPath = defaultWebappStaticUiPath
	}
	// it's important to coerce to empty string when the path is just a /
	// in most cases this is a typo provided by the user that will affect
	// every part of the application using the api url to construct links
	if apiRawURL.Path == "/" {
		apiRawURL.Path = ""
	}
	var webhookAppURL *url.URL
	if svixAppURL := os.Getenv("WEBHOOK_APPURL"); svixAppURL != "" {
		webhookAppURL, err = url.Parse(svixAppURL)
		if err != nil {
			return fmt.Errorf("failed parsing WEBHOOK_APPURL, reason=%v", err)
		}
	}

	grpcClientTLSCa, err := envloader.GetEnv("HOOP_TLSCA")
	if err != nil {
		return fmt.Errorf("failed loading env HOOP_TLSCA, reason=%v", err)
	}
	gatewayTLSKey, err := envloader.GetEnv("TLS_KEY")
	if err != nil {
		return fmt.Errorf("failed loading env TLS_KEY, reason=%v", err)
	}
	gatewayTLSCert, err := envloader.GetEnv("TLS_CERT")
	if err != nil {
		return fmt.Errorf("failed loading env TLS_CERT, reason=%v", err)
	}

	hasRedactCredentials := func() bool {
		dlpProvider := os.Getenv("DLP_PROVIDER")
		if dlpProvider == "" || dlpProvider == "gcp" {
			return isEnvSet("GOOGLE_APPLICATION_CREDENTIALS_JSON")
		}

		if dlpProvider == "mspresidio" {
			return isEnvSet("MSPRESIDIO_ANALYZER_URL") && isEnvSet("MSPRESIDIO_ANONYMIZER_URL")
		}

		return false
	}()

	sshClientHostKey := os.Getenv("SSH_CLIENT_HOST_KEY")
	if sshClientHostKey != "" {
		if _, err := base64.StdEncoding.DecodeString(sshClientHostKey); err != nil {
			return fmt.Errorf("failed decoding env SSH_CLIENT_HOST_KEY, err=%v", err)
		}
	}
	dlpMode := os.Getenv("DLP_MODE")
	if dlpMode == "" {
		dlpMode = "best-effort"
	}

	allowPlainText := os.Getenv("GATEWAY_ALLOW_PLAINTEXT") != "false" // Defaults to true
	// For backwards compatibility, we also allow plaintext if no TLS envs are set
	gatewayUseTLS := os.Getenv("USE_TLS") == "true" || grpcClientTLSCa != "" || gatewayTLSKey != "" || gatewayTLSCert != ""
	gatewaySkipTLSVerify := os.Getenv("HOOP_TLS_SKIP_VERIFY") == "true"

	spiffeMode, spiffeBundleURL, spiffeBundleFile, spiffeBundleJWKS, spiffeTrustDomain, spiffeAudience, spiffeRefreshPeriod, err := loadSPIFFEConfig()
	if err != nil {
		return err
	}

	runtimeConfig = Config{
		apiKey:                          os.Getenv("API_KEY"),
		apiURL:                          fmt.Sprintf("%s://%s", apiRawURL.Scheme, apiRawURL.Host),
		grpcURL:                         grpcURL,
		apiHostname:                     apiRawURL.Hostname(),
		apiScheme:                       apiRawURL.Scheme,
		apiHost:                         apiRawURL.Host,
		apiURLPath:                      apiRawURL.Path,
		authMethod:                      authMethod,
		askAICredentials:                askAICred,
		pgCred:                          pgCred,
		migrationPathFiles:              migrationPathFiles,
		licenseSigningKey:               licensePrivKey,
		licenseSignerOrgID:              allowedOrgID,
		gcpDLPJsonCredentials:           gcpJsonCred,
		orgMultitenant:                  os.Getenv("ORG_MULTI_TENANT") == "true",
		analyticsTracking:               true, // it's set on app startup
		dlpProvider:                     os.Getenv("DLP_PROVIDER"),
		dlpMode:                         dlpMode,
		hasRedactCredentials:            hasRedactCredentials,
		msPresidioAnalyzerURL:           os.Getenv("MSPRESIDIO_ANALYZER_URL"),
		msPresidioAnonymizerURL:         os.Getenv("MSPRESIDIO_ANONYMIZER_URL"),
		webhookAppKey:                   os.Getenv("WEBHOOK_APPKEY"),
		webhookAppURL:                   webhookAppURL,
		webappUsersManagement:           webappUsersManagement,
		webappStaticUIPath:              webappStaticUiPath,
		isLoaded:                        true,
		disableSessionsDownload:         os.Getenv("DISABLE_SESSIONS_DOWNLOAD") == "true",
		disableClipboardCopyCut:         os.Getenv("DISABLE_CLIPBOARD_COPY_CUT") == "true",
		gatewayUseTLS:                   gatewayUseTLS,
		grpcClientTLSCa:                 grpcClientTLSCa,
		gatewayTLSKey:                   gatewayTLSKey,
		gatewayTLSCert:                  gatewayTLSCert,
		gatewayAllowPlainText:           allowPlainText,
		gatewaySkipTLSVerify:            gatewaySkipTLSVerify,
		sshClientHostKey:                sshClientHostKey,
		integrationAWSInstanceRoleAllow: os.Getenv("INTEGRATION_AWS_INSTANCE_ROLE_ALLOW") == "true",
		// Temporary solution to force token exchange through URL, because the JWT could be too large for cookies.
		// This will be removed in future versions
		forceUrlTokenExchange: os.Getenv("URL_TOKEN_EXCHANGE") == "force",

		spiffeMode:          spiffeMode,
		spiffeBundleURL:     spiffeBundleURL,
		spiffeBundleFile:    spiffeBundleFile,
		spiffeBundleJWKS:    spiffeBundleJWKS,
		spiffeTrustDomain:   spiffeTrustDomain,
		spiffeAudience:      spiffeAudience,
		spiffeRefreshPeriod: spiffeRefreshPeriod,
	}
	return nil
}

func Get() Config     { return runtimeConfig }
func GetRef() *Config { return &runtimeConfig }

// maintains compatibility by loading oidc auth method when
// the IDP_ envs are set.
//
// Load the local auth method when it's not set
func loadAuthMethod() (authMethod idptypes.ProviderType, err error) {
	authMethod = idptypes.ProviderType(os.Getenv("AUTH_METHOD"))
	if hasIdpEnvs() && authMethod == "" {
		authMethod = idptypes.ProviderTypeOIDC
		return
	}

	if authMethod == "" || authMethod == idptypes.ProviderTypeLocal {
		authMethod = idptypes.ProviderTypeLocal
	}

	switch authMethod {
	case idptypes.ProviderTypeOIDC, idptypes.ProviderTypeIDP:
	case idptypes.ProviderTypeSAML:
	case idptypes.ProviderTypeLocal, idptypes.ProviderType(""):
	default:
		return idptypes.ProviderType(""), fmt.Errorf("invalid AUTH_METHOD env, got=%v", authMethod)
	}
	return
}

func hasIdpEnvs() bool {
	return os.Getenv("IDP_ISSUER") != "" ||
		os.Getenv("IDP_CLIENT_ID") != "" ||
		os.Getenv("IDP_CLIENT_SECRET") != "" ||
		os.Getenv("IDP_URI") != ""
}

func loadPostgresCredentials() (*pgCredentials, error) {
	pgConnectionURI := os.Getenv("POSTGRES_DB_URI")
	if pgConnectionURI == "" {
		pgConnectionURI = "postgres://postgres:postgres@localhost:5432/hoop?sslmode=disable"
	}
	pgURL, err := url.Parse(pgConnectionURI)
	if err != nil {
		return nil, fmt.Errorf("failed parsing POSTGRES_DB_URI, err=%v", err)
	}

	var pgUser, pgPassword string
	if pgURL.User != nil {
		pgUser = pgURL.User.Username()
		pgPassword, _ = pgURL.User.Password()
	}
	if pgUser == "" || pgPassword == "" {
		return nil, fmt.Errorf("missing user or password in POSTGRES_DB_URI env")
	}
	return &pgCredentials{connectionString: pgConnectionURI, username: pgUser}, nil
}

func loadAskAICredentials() (*url.URL, error) {
	askAICred := os.Getenv("ASK_AI_CREDENTIALS")
	if askAICred == "" {
		return nil, nil
	}
	u, err := url.Parse(askAICred)
	if err != nil {
		return nil, fmt.Errorf("ASK_AI_CREDENTIALS env is not in a valid configuration format: %v", err)
	}
	if u.User == nil {
		return nil, fmt.Errorf("ASK_AI_CREDENTIALS env is missing the api key")
	}
	if apiKey, _ := u.User.Password(); apiKey == "" {
		return nil, fmt.Errorf("ASK_AI_CREDENTIALS env is missing the api key")
	}
	return u, nil
}

func loadGcpDLPCredentials() (string, error) {
	jsonCred := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if jsonCred == "" {
		return "", nil
	}
	var js json.RawMessage
	if err := json.Unmarshal([]byte(jsonCred), &js); err != nil {
		return "", fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON is not in json format, failed parsing: %v", err)
	}
	return jsonCred, nil
}

func loadLicensePrivateKey() (string, *rsa.PrivateKey, error) {
	signingKeyCredentials := os.Getenv("LICENSE_SIGNING_KEY")
	if signingKeyCredentials == "" {
		return "", nil, nil
	}
	allowedOrgID, b64EncPrivateKey, found := strings.Cut(signingKeyCredentials, ",")
	if !found || allowedOrgID == "" {
		return "", nil, nil
	}

	privKeyBytes, err := base64.StdEncoding.DecodeString(b64EncPrivateKey)
	if err != nil {
		return "", nil, fmt.Errorf("unable to load LICENSE_SIGNING_KEY, reason=%v", err)
	}
	block, _ := pem.Decode(privKeyBytes)
	obj, _ := x509.ParsePKCS8PrivateKey(block.Bytes)
	privkey, ok := obj.(*rsa.PrivateKey)
	if !ok {
		return "", nil, fmt.Errorf("unable to load LICENSE_SIGNING_KEY: it is not a private key, got=%T", obj)
	}
	return allowedOrgID, privkey, nil
}

func (c Config) LicenseSigningKey() (string, *rsa.PrivateKey) {
	return c.licenseSignerOrgID, c.licenseSigningKey
}

func (c *Config) SetAnalyticsTracking(enabled bool) { c.analyticsTracking = enabled }

// FullApiURL returns the full url which contains the path of the URL
func (c Config) FullApiURL() string { return c.apiURL + c.apiURLPath }

// ApiURL is the base URL without any path segment or query strings (scheme://host:port)
func (c Config) ApiURL() string                        { return c.apiURL }
func (c Config) GrpcURL() string                       { return c.grpcURL }
func (c Config) WebappStaticUiPath() string            { return c.webappStaticUIPath }
func (c Config) ApiHostname() string                   { return c.apiHostname }
func (c Config) ApiHost() string                       { return c.apiHost } // ApiHost host or host:port
func (c Config) ApiScheme() string                     { return c.apiScheme }
func (c Config) ApiURLPath() string                    { return c.apiURLPath }
func (c Config) ApiKey() string                        { return c.apiKey }
func (c Config) AuthMethod() idptypes.ProviderType     { return c.authMethod }
func (c Config) ForceUrlTokenExchange() bool           { return c.forceUrlTokenExchange }
func (c Config) WebhookAppKey() string                 { return c.webhookAppKey }
func (c Config) WebhookAppURL() *url.URL               { return c.webhookAppURL }
func (c Config) GcpDLPJsonCredentials() string         { return c.gcpDLPJsonCredentials }
func (c Config) DlpProvider() string                   { return c.dlpProvider }
func (c Config) DlpMode() string                       { return c.dlpMode }
func (c Config) HasRedactCredentials() bool            { return c.hasRedactCredentials }
func (c Config) MSPresidioAnalyzerURL() string         { return c.msPresidioAnalyzerURL }
func (c Config) MSPresidioAnomymizerURL() string       { return c.msPresidioAnonymizerURL }
func (c Config) PgUsername() string                    { return c.pgCred.username }
func (c Config) PgURI() string                         { return c.pgCred.connectionString }
func (c Config) AnalyticsTracking() bool               { return c.analyticsTracking }
func (c Config) DisableSessionsDownload() bool         { return c.disableSessionsDownload }
func (c Config) DisableClipboardCopyCut() bool         { return c.disableClipboardCopyCut }
func (c Config) MigrationPathFiles() string            { return c.migrationPathFiles }
func (c Config) OrgMultitenant() bool                  { return c.orgMultitenant }
func (c Config) WebappUsersManagement() string         { return c.webappUsersManagement }
func (c Config) IsAskAIAvailable() bool                { return c.askAICredentials != nil }
func (c Config) GatewayUseTLS() bool                   { return c.gatewayUseTLS }
func (c Config) GrpcClientTLSCa() string               { return c.grpcClientTLSCa }
func (c Config) GatewayTLSKey() string                 { return c.gatewayTLSKey }
func (c Config) GatewayTLSCert() string                { return c.gatewayTLSCert }
func (c Config) GatewaySkipTLSVerify() bool            { return c.gatewaySkipTLSVerify }
func (c Config) SSHClientHostKey() string              { return c.sshClientHostKey }
func (c Config) IntegrationAWSInstanceRoleAllow() bool { return c.integrationAWSInstanceRoleAllow }
func (c Config) AskAIApiURL() (u string) {
	if c.IsAskAIAvailable() {
		return fmt.Sprintf("%s://%s", c.askAICredentials.Scheme, c.askAICredentials.Host)
	}
	return ""
}

func (c Config) AskAIAPIKey() (t string) {
	if c.IsAskAIAvailable() {
		t, _ = c.askAICredentials.User.Password()
	}
	return
}

func isEnvSet(key string) bool {
	val, isset := os.LookupEnv(key)
	return isset && val != ""
}

// SPIFFE mode values.
//
//	disabled: no SPIFFE validation at all (default).
//	enforce:  validate JWT-SVIDs presented. Reject on failure. Static-token agents
//	          continue to work in parallel (identified by token shape, not by policy).
//	          Bad HOOP_SPIFFE_* env values fail startup; a failed initial bundle
//	          fetch logs a warning and is retried by the background refresh loop
//	          so an unreachable bundle does not take DSN agents offline.
const (
	SPIFFEModeDisabled = "disabled"
	SPIFFEModeEnforce  = "enforce"
)

// loadSPIFFEConfig reads and validates SPIFFE-related env vars. All fields are
// optional; when mode is "disabled" the other fields are ignored.
//
//	HOOP_SPIFFE_MODE             disabled|enforce (default: disabled)
//	HOOP_SPIFFE_BUNDLE_URL       HTTPS URL to fetch the SPIFFE trust bundle (JWKS-shaped)
//	HOOP_SPIFFE_BUNDLE_FILE      path to a static trust bundle file (JWKS JSON)
//	HOOP_SPIFFE_BUNDLE_JWKS      inline SPIFFE trust bundle as JWKS JSON. Accepts
//	                             either raw JSON (auto-detected by a leading '{')
//	                             or standard-encoded base64 of the JWKS JSON.
//	                             Intended for cases where mounting a file is
//	                             inconvenient (e.g. pure env-based deployments).
//	HOOP_SPIFFE_TRUST_DOMAIN     trust domain name, e.g. "customer.com"
//	HOOP_SPIFFE_AUDIENCE         required audience claim on JWT-SVIDs (default: "hoop-gateway")
//	HOOP_SPIFFE_REFRESH_PERIOD   go duration for trust bundle refresh (default: 30s)
func loadSPIFFEConfig() (mode, bundleURL, bundleFile, bundleJWKS, trustDomain, audience string, refresh time.Duration, err error) {
	mode = os.Getenv("HOOP_SPIFFE_MODE")
	if mode == "" {
		mode = SPIFFEModeDisabled
	}
	switch mode {
	case SPIFFEModeDisabled, SPIFFEModeEnforce:
	default:
		err = fmt.Errorf("invalid HOOP_SPIFFE_MODE, got=%q, expected disabled|enforce", mode)
		return
	}

	if mode == SPIFFEModeDisabled {
		return
	}

	bundleURL = os.Getenv("HOOP_SPIFFE_BUNDLE_URL")
	bundleFile = os.Getenv("HOOP_SPIFFE_BUNDLE_FILE")
	rawJWKS := strings.TrimSpace(os.Getenv("HOOP_SPIFFE_BUNDLE_JWKS"))
	trustDomain = os.Getenv("HOOP_SPIFFE_TRUST_DOMAIN")
	audience = os.Getenv("HOOP_SPIFFE_AUDIENCE")
	if audience == "" {
		audience = "hoop-gateway"
	}

	// HOOP_SPIFFE_BUNDLE_JWKS accepts either raw JWKS JSON or base64-encoded
	// JWKS JSON. Raw JSON is detected by a leading '{'. Anything else is
	// assumed to be base64 and decoded here so downstream code only deals
	// with JSON bytes. We validate the shape (must start with '{' after
	// decode) to fail fast on malformed input rather than at first SVID
	// validation.
	if rawJWKS != "" {
		if strings.HasPrefix(rawJWKS, "{") {
			bundleJWKS = rawJWKS
		} else {
			decoded, decErr := base64.StdEncoding.DecodeString(rawJWKS)
			if decErr != nil {
				err = fmt.Errorf("failed decoding HOOP_SPIFFE_BUNDLE_JWKS as base64: %v", decErr)
				return
			}
			trimmed := strings.TrimSpace(string(decoded))
			if !strings.HasPrefix(trimmed, "{") {
				err = fmt.Errorf("HOOP_SPIFFE_BUNDLE_JWKS did not decode to a JSON object")
				return
			}
			bundleJWKS = trimmed
		}
	}

	// exactly one source must be set
	sourcesSet := 0
	if bundleURL != "" {
		sourcesSet++
	}
	if bundleFile != "" {
		sourcesSet++
	}
	if bundleJWKS != "" {
		sourcesSet++
	}
	if sourcesSet != 1 {
		err = fmt.Errorf("SPIFFE mode %q requires exactly one of HOOP_SPIFFE_BUNDLE_URL, HOOP_SPIFFE_BUNDLE_FILE, or HOOP_SPIFFE_BUNDLE_JWKS to be set", mode)
		return
	}
	if trustDomain == "" {
		err = fmt.Errorf("SPIFFE mode %q requires HOOP_SPIFFE_TRUST_DOMAIN to be set", mode)
		return
	}

	refresh = 30 * time.Second
	if v := os.Getenv("HOOP_SPIFFE_REFRESH_PERIOD"); v != "" {
		refresh, err = time.ParseDuration(v)
		if err != nil {
			err = fmt.Errorf("failed parsing HOOP_SPIFFE_REFRESH_PERIOD, reason=%v", err)
			return
		}
		if refresh < time.Second {
			err = fmt.Errorf("HOOP_SPIFFE_REFRESH_PERIOD must be at least 1s, got=%v", refresh)
			return
		}
	}

	return
}

func (c Config) SPIFFEMode() string                 { return c.spiffeMode }
func (c Config) SPIFFEEnabled() bool                { return c.spiffeMode == SPIFFEModeEnforce }
func (c Config) SPIFFEBundleURL() string            { return c.spiffeBundleURL }
func (c Config) SPIFFEBundleFile() string           { return c.spiffeBundleFile }
func (c Config) SPIFFEBundleJWKS() string           { return c.spiffeBundleJWKS }
func (c Config) SPIFFETrustDomain() string          { return c.spiffeTrustDomain }
func (c Config) SPIFFEAudience() string             { return c.spiffeAudience }
func (c Config) SPIFFERefreshPeriod() time.Duration { return c.spiffeRefreshPeriod }
