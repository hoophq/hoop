package admin

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/spf13/cobra"
)

const sshServerMiscEndpoint = "/api/serverconfig/misc"

var (
	sshListenAddrFlag string
	sshHostsKeyFlag   string
	sshTrustedCAsFlag []string
	sshCertAttrFlag   string
	sshUserAttrFlag   string
	sshOutputFlag     string
)

func init() {
	sshServerCmd.AddCommand(sshServerGetCmd)
	sshServerCmd.AddCommand(sshServerApplyCmd)

	sshServerApplyCmd.Flags().StringVarP(&sshListenAddrFlag, "listen-address", "l", "",
		"Address for the SSH proxy server (e.g. 0.0.0.0:12222). Empty value disables the server.")
	sshServerApplyCmd.Flags().StringVar(&sshHostsKeyFlag, "hosts-key", "",
		"Base64-encoded PEM private key used as the SSH host key. "+
			"If omitted, the existing key is preserved (or auto-generated for new deployments).")
	sshServerApplyCmd.Flags().StringArrayVar(&sshTrustedCAsFlag, "trusted-ca", nil,
		"Trusted SSH CA public key in authorized_keys format. "+
			"May be repeated for multiple CAs. Enables certificate authentication when set.")
	sshServerApplyCmd.Flags().StringVar(&sshCertAttrFlag, "cert-attr", "",
		"Certificate attribute to match against a Hoop user. One of: principal, key_id. "+
			"Required when --trusted-ca is set.")
	sshServerApplyCmd.Flags().StringVar(&sshUserAttrFlag, "user-attr", "",
		"Hoop user attribute matched against the certificate attribute. One of: email, subject, user_id. "+
			"Required when --trusted-ca is set.")

	sshServerGetCmd.Flags().StringVarP(&sshOutputFlag, "output", "o", "",
		"Output format. One of: (json)")
}

var sshServerCmd = &cobra.Command{
	Use:   "sshserver",
	Short: "Manage the SSH proxy server configuration",
}

var sshServerGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Display the current SSH proxy server configuration",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		conf := clientconfig.GetClientConfigOrDie()
		decodeTo := "object"
		if sshOutputFlag == "json" {
			decodeTo = "raw"
		}
		obj, _, err := httpRequest(&apiResource{
			suffixEndpoint: sshServerMiscEndpoint,
			conf:           conf,
			decodeTo:       decodeTo,
		})
		if err != nil {
			fmt.Println(styles.ClientErrorSimple(fmt.Sprintf("failed getting server config: %v", err)))
			return
		}
		if sshOutputFlag == "json" {
			rawData, _ := obj.([]byte)
			// Extract and re-encode only the SSH section for a focused output
			var full map[string]any
			if err := json.Unmarshal(rawData, &full); err != nil {
				fmt.Println(string(rawData))
				return
			}
			sshSection := full["ssh_server_config"]
			out, _ := json.MarshalIndent(sshSection, "", "  ")
			fmt.Println(string(out))
			return
		}
		resp, _ := obj.(map[string]any)
		sshCfg, _ := resp["ssh_server_config"].(map[string]any)
		printSSHServerConfig(cmd, sshCfg)
	},
}

var sshServerApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Replace the SSH proxy server configuration",
	Long: `Replace all SSH proxy server configuration with the provided flags.

The hosts key is preserved from the current configuration when --hosts-key is
not specified, to avoid breaking existing SSH clients that have already
accepted the host fingerprint.

Setting --listen-address to an empty string disables the SSH proxy server.

Examples:

  # Enable the SSH proxy on port 2222 (auto-generates a hosts key on first run)
  hoop admin sshserver apply --listen-address 0.0.0.0:2222

  # Enable certificate authentication with two trusted CAs
  hoop admin sshserver apply --listen-address 0.0.0.0:2222 \
    --trusted-ca "$(cat ca1.pub)" \
    --trusted-ca "$(cat ca2.pub)"

  # Disable the SSH proxy server
  hoop admin sshserver apply
`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		conf := clientconfig.GetClientConfigOrDie()

		// GET the full misc config first so unrelated proxy settings are preserved
		// and the existing hosts key is not discarded.
		obj, _, err := httpRequest(&apiResource{
			suffixEndpoint: sshServerMiscEndpoint,
			conf:           conf,
			decodeTo:       "object",
		})
		if err != nil {
			fmt.Println(styles.ClientErrorSimple(fmt.Sprintf("failed loading current server config: %v", err)))
			return
		}
		current, _ := obj.(map[string]any)
		if current == nil {
			current = map[string]any{}
		}

		// Preserve the existing hosts key when --hosts-key is not provided, so
		// clients that already accepted the host fingerprint are not broken.
		hostsKey := sshHostsKeyFlag
		if hostsKey == "" {
			if existingSSH, ok := current["ssh_server_config"].(map[string]any); ok {
				if existing, ok := existingSSH["hosts_key"].(string); ok {
					hostsKey = existing
				}
			}
		}

		newSSHConfig := buildSSHConfigPayload(sshListenAddrFlag, hostsKey, sshTrustedCAsFlag, sshCertAttrFlag, sshUserAttrFlag)
		current["ssh_server_config"] = newSSHConfig

		result, err := httpBodyRequest(&apiResource{
			suffixEndpoint: sshServerMiscEndpoint,
			conf:           conf,
			decodeTo:       "object",
		}, "PUT", current)
		if err != nil {
			fmt.Println(styles.ClientErrorSimple(fmt.Sprintf("failed applying SSH server config: %v", err)))
			return
		}

		respMap, _ := result.(map[string]any)
		sshCfg, _ := respMap["ssh_server_config"].(map[string]any)

		printSSHServerConfig(cmd, sshCfg)
	},
}

// buildSSHConfigPayload constructs the ssh_server_config map sent to the API.
// A nil trustedCAs slice means "no certificate auth"; an empty (non-nil) slice
// from --trusted-ca flags removes all previously configured CAs.
func buildSSHConfigPayload(listenAddr, hostsKey string, trustedCAs []string, certAttr, userAttr string) map[string]any {
	cfg := map[string]any{
		"listen_address": listenAddr,
		"hosts_key":      hostsKey,
	}
	if len(trustedCAs) > 0 {
		cfg["trusted_cas"] = trustedCAs
	}
	if certAttr != "" && userAttr != "" {
		cfg["user_mapping"] = map[string]any{
			"cert_attr": certAttr,
			"user_attr": userAttr,
		}
	}
	return cfg
}

func printSSHServerConfig(cmd *cobra.Command, cfg map[string]any) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")

	var listenAddr, hostsKey string
	var trustedCAs []any
	var userMapping map[string]any
	if cfg != nil {
		listenAddr, _ = cfg["listen_address"].(string)
		hostsKey, _ = cfg["hosts_key"].(string)
		trustedCAs, _ = cfg["trusted_cas"].([]any)
		userMapping, _ = cfg["user_mapping"].(map[string]any)
	}

	status := "enabled"
	if listenAddr == "" {
		status = "disabled"
	}

	hostsKeyDisplay := "not set"
	if hostsKey != "" {
		hostsKeyDisplay = "set"
	}

	trustedCAsDisplay := "none (password auth only)"
	if len(trustedCAs) > 0 {
		trustedCAsDisplay = fmt.Sprintf("%d CA(s) configured", len(trustedCAs))
	}

	listenAddrDisplay := listenAddr
	if listenAddrDisplay == "" {
		listenAddrDisplay = "-"
	}

	certAttrDisplay := "-"
	userAttrDisplay := "-"
	if userMapping != nil {
		if v, _ := userMapping["cert_attr"].(string); v != "" {
			certAttrDisplay = v
		}
		if v, _ := userMapping["user_attr"].(string); v != "" {
			userAttrDisplay = v
		}
	}

	fmt.Fprintf(w, "Status\t%s\n", status)
	fmt.Fprintf(w, "Listen Address\t%s\n", listenAddrDisplay)
	fmt.Fprintf(w, "Hosts Key\t%s\n", hostsKeyDisplay)
	fmt.Fprintf(w, "Trusted CAs\t%s\n", trustedCAsDisplay)
	fmt.Fprintf(w, "Cert Attr\t%s\n", certAttrDisplay)
	fmt.Fprintf(w, "User Attr\t%s\n", userAttrDisplay)

	if len(trustedCAs) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "CA ENTRIES\t")
		for i, ca := range trustedCAs {
			caStr, _ := ca.(string)
			// Truncate long keys for readability; full value visible via --output json
			display := caStr
			if len(display) > 60 {
				display = display[:57] + "..."
			}
			fmt.Fprintf(w, "  [%d]\t%s\n", i+1, display)
		}
	}
	w.Flush()
}
