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
	"time"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

var sidecarCredentialExpires time.Duration

var sidecarCmd = &cobra.Command{
	Use:   "sidecar",
	Short: "Work with sidecar lanes",
}

var sidecarCredentialCmd = &cobra.Command{
	Use:   "credential SIDECAR/LANE",
	Short: "Mint a credential for a sidecar lane",
	Long: `Mints a key the sidecar lane accepts as your password, and prints it once.
An admin must have granted you, or one of your groups, the lane.`,
	Example: `hoop sidecar credential payments-sidecar/appdb
hoop sidecar credential payments-sidecar/appdb --expires 2h -o json`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runSidecarCredential(args[0], sidecarCredentialExpires, outputFlag == "json")
	},
}

func init() {
	sidecarCredentialCmd.Flags().DurationVar(&sidecarCredentialExpires, "expires", 24*time.Hour, "How long the credential lives, at most 720h")
	sidecarCredentialCmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output format. One of: (json)")
	sidecarCmd.AddCommand(sidecarCredentialCmd)
	rootCmd.AddCommand(sidecarCmd)
}

// sidecarCredentialResponse mirrors openapi.SidecarCredential.
type sidecarCredentialResponse struct {
	SidecarName  string    `json:"sidecar_name"`
	ListenerName string    `json:"listener_name"`
	Protocol     string    `json:"protocol"`
	User         string    `json:"user"`
	Key          string    `json:"key"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func runSidecarCredential(target string, expires time.Duration, jsonMode bool) {
	sidecar, lane, ok := strings.Cut(target, "/")
	if !ok || sidecar == "" || lane == "" {
		fatalErr(jsonMode, "expected SIDECAR/LANE, got %q", target)
	}
	hours := int((expires + time.Hour - 1) / time.Hour)
	if hours <= 0 {
		fatalErr(jsonMode, "--expires must be at least 1h")
	}
	config := clientconfig.GetClientConfigOrDie()
	rawBody, err := requestSidecarCredential(config, sidecar, lane, hours)
	if err != nil {
		fatalErr(jsonMode, "%s", err.Error())
	}
	if jsonMode {
		os.Stdout.Write(rawBody)
		if len(rawBody) > 0 && rawBody[len(rawBody)-1] != '\n' {
			fmt.Println()
		}
		return
	}
	var resp sidecarCredentialResponse
	if err := json.Unmarshal(rawBody, &resp); err != nil {
		fatalErr(false, "failed decoding response: %v", err)
	}
	fmt.Println(styles.Default.Render(fmt.Sprintf("Credential for %s/%s, shown once. Expires %s.",
		resp.SidecarName, resp.ListenerName, resp.ExpiresAt.Local().Format(time.RFC822))))
	printCredentialField("User", resp.User)
	printCredentialField("Key", resp.Key)
	if hint := sidecarCredentialHint(resp); hint != "" {
		fmt.Println()
		fmt.Println(hint)
	}
}

// sidecarCredentialHint is one line on how a native client presents the key
// to the lane's protocol.
func sidecarCredentialHint(resp sidecarCredentialResponse) string {
	switch resp.Protocol {
	case "postgres":
		return fmt.Sprintf("  psql \"host=<sidecar> user=%s password=%s sslmode=require\"", resp.User, resp.Key)
	case "mysql":
		return fmt.Sprintf("  mysql -h <sidecar> -u %s -p%s", resp.User, resp.Key)
	case "mongodb":
		return fmt.Sprintf("  mongosh \"mongodb://%s:%s@<sidecar>/?authMechanism=SCRAM-SHA-256\"", url.QueryEscape(resp.User), resp.Key)
	case "mssql":
		return fmt.Sprintf("  sqlcmd -S <sidecar> -U %s -P %s", resp.User, resp.Key)
	case "clickhouse":
		return fmt.Sprintf("  clickhouse-client --host <sidecar> --user %s --password %s", resp.User, resp.Key)
	case "oracle":
		return fmt.Sprintf("  sqlplus %s/%s@<sidecar>", resp.User, resp.Key)
	case "http":
		return fmt.Sprintf("  curl -H 'Authorization: Bearer %s' https://<sidecar>/", resp.Key)
	}
	return ""
}

// requestSidecarCredential POSTs /sidecars/{sidecar}/lanes/{lane}/credentials
// and returns the raw body of a 201.
func requestSidecarCredential(config *clientconfig.Config, sidecar, lane string, hours int) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/api/sidecars/%s/lanes/%s/credentials",
		config.ApiURL, url.PathEscape(sidecar), url.PathEscape(lane))
	body := fmt.Appendf(nil, `{"expires_in_hours":%d}`, hours)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%v", version.Get().Version))

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed performing request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusCreated {
		return respBody, nil
	}
	var errBody struct {
		Message string `json:"message"`
	}
	if jsonErr := json.Unmarshal(respBody, &errBody); jsonErr == nil && errBody.Message != "" {
		return nil, fmt.Errorf("%s", errBody.Message)
	}
	return nil, fmt.Errorf("request failed (status=%d): %s", resp.StatusCode, string(respBody))
}
