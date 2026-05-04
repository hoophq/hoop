package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

var claudeSettingsFile string

var claudeCmd = &cobra.Command{
	Use:   "claude",
	Short: "Manage Claude Code integration",
}

var claudeConfigureCmd = &cobra.Command{
	Use:   "configure CONNECTION",
	Short: "Apply active native connection credentials to ~/.claude/settings.json",
	Long: `Reads the current active credentials for a claude-code connection and writes
them to ~/.claude/settings.json. The connection must already be open via the
webapp or 'hoop connect' — this command does not create a new session.`,
	Example: `hoop claude configure my-claude-conn
hoop claude configure my-claude-conn --file /custom/path/settings.json`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runClaudeConfigure(args[0])
	},
}

func init() {
	claudeConfigureCmd.Flags().StringVarP(&claudeSettingsFile, "file", "f", "", "Path to Claude settings file (default: ~/.claude/settings.json)")
	claudeCmd.AddCommand(claudeConfigureCmd)
}

func runClaudeConfigure(connectionName string) {
	config := clientconfig.GetClientConfigOrDie()

	creds, err := fetchActiveConnectionCredentials(config, connectionName)
	if err != nil {
		printErrorAndExit("%s", err.Error())
	}

	scheme := "http"
	baseURL := fmt.Sprintf("%s://%s:%s", scheme, creds.Hostname, creds.Port)
	proxyToken := creds.ProxyToken

	if err := updateClaudeSettings(baseURL, proxyToken, claudeSettingsFile); err != nil {
		printErrorAndExit("failed to update Claude settings: %s", err.Error())
	}

	path, _ := claudeSettingsFilePath(claudeSettingsFile)
	printClaudeConfigureSuccess(path, connectionName, baseURL, proxyToken)
}

func printClaudeConfigureSuccess(settingsPath, connectionName, baseURL, token string) {
	labelStyle := lipgloss.NewStyle().Faint(true).Width(14)
	valueStyle := lipgloss.NewStyle()
	successStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))

	// truncate token for display
	displayToken := token
	if len(token) > 24 {
		displayToken = token[:24] + "..."
	}

	fmt.Println()
	fmt.Printf("  %s %s\n", successStyle.Render("✓"), styles.Fainted(settingsPath+" updated"))
	fmt.Println()
	fmt.Printf("  %s%s\n", labelStyle.Render("Connection"), valueStyle.Render(connectionName))
	fmt.Printf("  %s%s\n", labelStyle.Render("Base URL"), valueStyle.Render(baseURL))
	fmt.Printf("  %s%s\n", labelStyle.Render("Token"), valueStyle.Render(displayToken))
	fmt.Println()
	fmt.Println(styles.Fainted("  Claude Code requests are now routed through hoop."))
	fmt.Println(styles.Fainted("  Run " + strings.TrimSpace(styles.Keyword(" claude ")) + styles.Fainted(" to start.")))
	fmt.Println()
}

type activeCredentials struct {
	Hostname   string `json:"hostname"`
	Port       string `json:"port"`
	ProxyToken string `json:"proxy_token"`
}

type credentialsEnvelope struct {
	ConnectionCredentials activeCredentials `json:"connection_credentials"`
}

func fetchActiveConnectionCredentials(config *clientconfig.Config, connectionName string) (*activeCredentials, error) {
	url := fmt.Sprintf("%s/api/connections/%s/credentials", config.ApiURL, connectionName)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%v", version.Get().Version))

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed performing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		respBody, _ := io.ReadAll(resp.Body)
		var body struct {
			Message string `json:"message"`
		}
		if jsonErr := json.Unmarshal(respBody, &body); jsonErr == nil && body.Message != "" {
			return nil, fmt.Errorf("%s", body.Message)
		}
		return nil, fmt.Errorf("no active credentials found for %q; open a native connection first", connectionName)
	}

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed (status=%d): %s", resp.StatusCode, string(respBody))
	}

	var envelope credentialsEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("failed decoding response: %w", err)
	}

	creds := &envelope.ConnectionCredentials
	if creds.Hostname == "" || creds.Port == "" {
		return nil, fmt.Errorf("connection %q is not of type claude-code or the proxy server is not configured", connectionName)
	}

	return creds, nil
}
