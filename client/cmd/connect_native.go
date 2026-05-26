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
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

// connectNativeOutput holds the --output flag value for hoop connect-native.
var connectNativeOutput string

var connectNativeExampleDesc = `hoop connect-native postgres-demo
hoop connect-native ssh-prod-bastion
hoop connect-native prod-cluster --output json
`

var connectNativeCmd = &cobra.Command{
	Use:   "connect-native CONNECTION",
	Short: "Print credentials to access a resource from a native client (psql, ssh, kubectl, ...)",
	Long: `Print credentials and connection instructions for a native-client connection.

Unlike 'hoop connect' (which opens a live tunneled session bound to the CLI process),
this command returns persistent credentials that you paste into your native tool —
psql, DBeaver, ssh, kubectl, and so on. The credentials are stable across calls and
remain usable as long as you stay authenticated with Hoop.

Supported subtypes: postgres, ssh, kubernetes.
For other native-client subtypes (rdp, aws-ssm, claude-code, ...) use the Hoop webapp.`,
	Example:      connectNativeExampleDesc,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		runConnectNative(args[0])
	},
}

func init() {
	connectNativeCmd.Flags().StringVarP(&connectNativeOutput, "output", "o", "", "Output format. One of: (json)")
	rootCmd.AddCommand(connectNativeCmd)
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
	// example URLs. We parse it to recover the scheme (http vs https) that the
	// gateway chose for this resource — derived server-side from the gateway's
	// TLS configuration.
	Command string `json:"command"`
}

type httpProxyCommands struct {
	Browser string `json:"browser"`
}

func runConnectNative(connectionName string) {
	jsonMode := connectNativeOutput == "json"
	config := clientconfig.GetClientConfigOrDie()

	rawBody, status, err := requestNativeCredentials(config, connectionName)
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
	case "ssh", "git", "github":
		renderSSH(&resp)
	case "kubernetes", "kubernetes-eks":
		renderKubernetes(&resp)
	default:
		fatalErr(false,
			"subtype %q is not yet supported by 'hoop connect-native'.\nUse the Hoop webapp to access this resource for now.",
			resp.ConnectionSubType)
	}
}

// requestNativeCredentials POSTs /connections/{name}/credentials with an empty
// body. The endpoint mints a persistent credential when none exists and
// returns the existing one otherwise — both paths return the same JSON shape.
// Returns the raw response body and HTTP status so callers can both echo it
// verbatim (--output json) and parse it (human rendering).
func requestNativeCredentials(config *clientconfig.Config, connectionName string) ([]byte, int, error) {
	url := fmt.Sprintf("%s/api/connections/%s/credentials", config.ApiURL, connectionName)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString("{}"))
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

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusAccepted {
		return body, resp.StatusCode, nil
	}

	// Try to surface the backend's error message.
	var errBody struct {
		Message string `json:"message"`
	}
	if jsonErr := json.Unmarshal(body, &errBody); jsonErr == nil && errBody.Message != "" {
		return nil, resp.StatusCode, fmt.Errorf("%s", errBody.Message)
	}
	return nil, resp.StatusCode, fmt.Errorf("request failed (status=%d): %s", resp.StatusCode, string(body))
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

	printHeader(resp.ConnectionName, resp.ConnectionSubType)
	printField("host", creds.Hostname)
	printField("port", creds.Port)
	printField("user", creds.Username)
	printField("password", creds.Password)
	printField("database", creds.DatabaseName)
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

	printHeader(resp.ConnectionName, resp.ConnectionSubType)
	printField("host", creds.Hostname)
	printField("port", creds.Port)
	printField("user", creds.Username)
	printField("password", creds.Password)
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

	printHeader(resp.ConnectionName, resp.ConnectionSubType)
	printField("server", server)
	printField("token", creds.ProxyToken)
	fmt.Println()
	fmt.Println(styles.Fainted("  Save the following as ~/.kube/%s.yaml, then:", clusterName))
	fmt.Printf("    export KUBECONFIG=~/.kube/%s.yaml\n", clusterName)
	fmt.Println()
	for line := range strings.SplitSeq(strings.TrimRight(kubeconfig, "\n"), "\n") {
		fmt.Printf("    %s\n", line)
	}
	fmt.Println()
}

func printHeader(name, subtype string) {
	fmt.Println()
	fmt.Printf("  %s %s\n", styles.Keyword(fmt.Sprintf(" %s ", name)), styles.Fainted("(%s)", subtype))
	fmt.Println()
}

func printField(label, value string) {
	fmt.Printf("  %-10s %s\n", label, value)
}

// schemeFromCommandBlob extracts the URL scheme the gateway uses (http or https)
// from the JSON-stringified "command" field returned for httpproxy/kubernetes
// connections. Falls back to "https" if the blob is missing or unparseable —
// production gateways serve native-client traffic over TLS.
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
