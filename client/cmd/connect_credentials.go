package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/common/version"
)

// persistentCredentialTypes lists the connection types that the
// --persistent-credential flow currently supports. The corresponding gateway
// proxies (postgres, ssh, http-based kubernetes) accept credentials minted
// via POST /api/connections/{name}/credentials. Extend this set as backend
// support for additional subtypes lands.
var persistentCredentialTypes = map[pb.ConnectionType]bool{
	pb.ConnectionTypePostgres:   true,
	pb.ConnectionTypeSSH:        true,
	pb.ConnectionTypeKubernetes: true,
}

// supportsPersistentCredential reports whether the given connection type can
// be served by the persistent-credential flow.
func supportsPersistentCredential(t pb.ConnectionType) bool {
	return persistentCredentialTypes[t]
}

// printPersistentCredentialTip writes a single-line hint to stderr suggesting
// the --persistent-credential flag. Stderr keeps the tip out of stdout
// pipelines that parse the credentials block.
func printPersistentCredentialTip(connectionName string) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, styles.Fainted(
		"Tip: use persistent credentials without keeping this CLI running:\n"+
			"     hoop connect %s --persistent-credential",
		connectionName))
}

// credentialsResponse mirrors openapi.ConnectionCredentialsResponse with only
// the fields the CLI cares about. connection_credentials is kept as a
// RawMessage and unmarshalled per-subtype.
type credentialsResponse struct {
	ConnectionName        string          `json:"connection_name"`
	ConnectionType        string          `json:"connection_type"`
	ConnectionSubType     string          `json:"connection_subtype"`
	ConnectionCredentials json.RawMessage `json:"connection_credentials"`
	SessionID             string          `json:"session_id"`
	HasReview             bool            `json:"has_review"`
	ReviewID              string          `json:"review_id"`
}

type postgresCreds struct {
	Hostname         string `json:"hostname"`
	Port             string `json:"port"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	DatabaseName     string `json:"database_name"`
	ConnectionString string `json:"connection_string"`
}

type sshCreds struct {
	Hostname string `json:"hostname"`
	Port     string `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type httpProxyCreds struct {
	Hostname   string `json:"hostname"`
	Port       string `json:"port"`
	ProxyToken string `json:"proxy_token"`
	// Command is a JSON-stringified blob with "curl", "browser", and "subdomain"
	// example URLs. We parse it to recover the scheme (http vs https) the
	// gateway chose for this resource — derived server-side from the gateway's
	// TLS configuration.
	Command string `json:"command"`
}

type httpProxyCommands struct {
	Browser string `json:"browser"`
}

// runPersistentCredentialFlow handles the --persistent-credential branch of
// `hoop connect`: it POSTs to /api/connections/{name}/credentials, prints the
// credentials (or a review-required notice), and exits. No local tunnel is
// opened — the credentials are meant to be pasted into a native client
// (DBeaver, psql, kubectl, ...) that can reach the gateway directly.
//
// accessDurationSec=0 issues a persistent (no-expiration) credential;
// passing a positive value (via -d/--duration) mints a bounded credential.
func runPersistentCredentialFlow(connectionName string, accessDurationSec int, jsonMode bool) {
	config := clientconfig.GetClientConfigOrDie()

	rawBody, status, err := requestPersistentCredential(config, connectionName, accessDurationSec)
	if err != nil {
		fatalErr(jsonMode, "%s", err.Error())
	}

	if jsonMode {
		// Echo the raw backend payload verbatim. Anything we synthesize on top
		// (kubeconfig, sshpass command) is shell-friendly only.
		os.Stdout.Write(rawBody)
		if len(rawBody) > 0 && rawBody[len(rawBody)-1] != '\n' {
			fmt.Println()
		}
		return
	}

	var resp credentialsResponse
	if err := json.Unmarshal(rawBody, &resp); err != nil {
		fatalErr(false, "failed decoding response: %v", err)
	}

	if status == http.StatusAccepted || resp.HasReview {
		renderReviewRequired(config, &resp)
		os.Exit(1)
	}

	switch resp.ConnectionSubType {
	case "postgres":
		renderPostgres(&resp)
	case "ssh":
		renderSSH(&resp)
	case "kubernetes":
		renderKubernetes(&resp)
	default:
		fatalErr(false,
			"subtype %q is not supported by --persistent-credential.\n"+
				"Re-run without the flag to use the legacy tunnel, or use the Hoop webapp.",
			resp.ConnectionSubType)
	}
}

// requestPersistentCredential POSTs /connections/{name}/credentials. The
// endpoint mints a persistent credential when none exists and returns the
// existing one otherwise — both paths return the same JSON shape. Returns
// the raw response body and HTTP status so callers can both echo it
// verbatim (--output json) and parse it (human rendering).
func requestPersistentCredential(config *clientconfig.Config, connectionName string, accessDurationSec int) ([]byte, int, error) {
	endpoint := fmt.Sprintf("%s/api/connections/%s/credentials", config.ApiURL, connectionName)

	body := []byte("{}")
	if accessDurationSec > 0 {
		body = fmt.Appendf(nil, `{"access_duration_seconds":%d}`, accessDurationSec)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("failed creating request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%v", version.Get().Version))

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed performing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusAccepted {
		return respBody, resp.StatusCode, nil
	}

	// Try to surface the backend's error message.
	var errBody struct {
		Message string `json:"message"`
	}
	if jsonErr := json.Unmarshal(respBody, &errBody); jsonErr == nil && errBody.Message != "" {
		return nil, resp.StatusCode, fmt.Errorf("%s", errBody.Message)
	}
	return nil, resp.StatusCode, fmt.Errorf("request failed (status=%d): %s", resp.StatusCode, string(respBody))
}

func renderReviewRequired(config *clientconfig.Config, resp *credentialsResponse) {
	reviewURL := fmt.Sprintf("%s/reviews/%s", config.ApiURL, resp.ReviewID)
	fmt.Println()
	fmt.Printf("  %s requires review approval.\n", styles.Keyword(fmt.Sprintf(" %s ", resp.ConnectionName)))
	fmt.Println()
	fmt.Printf("  Approve at: %s\n", reviewURL)
	fmt.Println()
	fmt.Println(styles.Fainted("  Re-run this command once the review has been approved."))
	fmt.Println()
}

func renderPostgres(resp *credentialsResponse) {
	var creds postgresCreds
	if err := json.Unmarshal(resp.ConnectionCredentials, &creds); err != nil {
		fatalErr(false, "failed decoding postgres credentials: %v", err)
	}

	printCredentialsHeader(resp.ConnectionName, resp.ConnectionSubType)
	printCredentialField("host", creds.Hostname)
	printCredentialField("port", creds.Port)
	printCredentialField("user", creds.Username)
	printCredentialField("password", creds.Password)
	printCredentialField("database", creds.DatabaseName)
	fmt.Println()
	fmt.Println(styles.Fainted("  Connect:"))
	fmt.Printf("    psql %q\n", creds.ConnectionString)
	fmt.Println()
}

func renderSSH(resp *credentialsResponse) {
	var creds sshCreds
	if err := json.Unmarshal(resp.ConnectionCredentials, &creds); err != nil {
		fatalErr(false, "failed decoding ssh credentials: %v", err)
	}

	printCredentialsHeader(resp.ConnectionName, resp.ConnectionSubType)
	printCredentialField("host", creds.Hostname)
	printCredentialField("port", creds.Port)
	printCredentialField("user", creds.Username)
	printCredentialField("password", creds.Password)
	fmt.Println()
	fmt.Println(styles.Fainted("  Connect (sshpass):"))
	fmt.Printf("    sshpass -p '%s' ssh %s@%s -p %s\n", creds.Password, creds.Username, creds.Hostname, creds.Port)
	fmt.Println()
	fmt.Println(styles.Fainted("  Without sshpass — paste the password when prompted:"))
	fmt.Printf("    ssh %s@%s -p %s\n", creds.Username, creds.Hostname, creds.Port)
	fmt.Println()
}

func renderKubernetes(resp *credentialsResponse) {
	var creds httpProxyCreds
	if err := json.Unmarshal(resp.ConnectionCredentials, &creds); err != nil {
		fatalErr(false, "failed decoding kubernetes credentials: %v", err)
	}

	clusterName := fmt.Sprintf("hoop-%s", resp.ConnectionName)
	scheme := schemeFromCommandBlob(creds.Command)
	server := fmt.Sprintf("%s://%s:%s", scheme, creds.Hostname, creds.Port)
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: %s
contexts:
- context:
    cluster: %s
    user: hoop
  name: %s
current-context: %s
users:
- name: hoop
  user:
    token: %s
`, server, clusterName, clusterName, clusterName, clusterName, creds.ProxyToken)

	printCredentialsHeader(resp.ConnectionName, resp.ConnectionSubType)
	printCredentialField("server", server)
	printCredentialField("token", creds.ProxyToken)
	fmt.Println()
	fmt.Println(styles.Fainted("  Save the following as ~/.kube/%s.yaml, then:", clusterName))
	fmt.Printf("    export KUBECONFIG=~/.kube/%s.yaml\n", clusterName)
	fmt.Println()
	for line := range strings.SplitSeq(strings.TrimRight(kubeconfig, "\n"), "\n") {
		fmt.Printf("    %s\n", line)
	}
	fmt.Println()
}

func printCredentialsHeader(name, subtype string) {
	fmt.Println()
	fmt.Printf("  %s %s\n", styles.Keyword(fmt.Sprintf(" %s ", name)), styles.Fainted("(%s)", subtype))
	fmt.Println()
}

func printCredentialField(label, value string) {
	fmt.Printf("  %-10s %s\n", label, value)
}

// schemeFromCommandBlob extracts the URL scheme the gateway uses (http or
// https) from the JSON-stringified "command" field returned for
// httpproxy/kubernetes connections. Falls back to "https" if the blob is
// missing or unparseable — production gateways serve native-client traffic
// over TLS.
func schemeFromCommandBlob(blob string) string {
	const fallback = "https"
	if blob == "" {
		return fallback
	}
	var cmds httpProxyCommands
	if err := json.Unmarshal([]byte(blob), &cmds); err != nil || cmds.Browser == "" {
		return fallback
	}
	parsed, err := url.Parse(cmds.Browser)
	if err != nil || parsed.Scheme == "" {
		return fallback
	}
	return parsed.Scheme
}
