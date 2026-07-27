// Federation admin CLI. Surfaces the four federation HTTP endpoints
// (GET/PUT/DELETE /connections/{name}/federation and POST /federation/test)
// as a focused subcommand tree so operators can manage IAM federation from
// the same place they manage every other admin resource.
//
// Layout decisions:
//
//   - A dedicated `hoop admin federation` subtree rather than extending
//     parseResourceOrDie. The endpoints are nested under connections, the
//     write path carries a write-only secret blob, and the dry-run verb has
//     no analog elsewhere in admin — three good reasons to escape the flat
//     <type>/<name> resource skeleton.
//
//   - --file (policy) and --credentials-file (secret) are intentionally
//     separate. The policy is safe to keep in git; the SA JSON must never
//     be. Mirroring this split in flags keeps the CLI honest about what is
//     committable and what is not.
//
//   - `set` is upsert with credential preservation: omitting
//     --credentials-file on an existing config leaves the stored ciphertext
//     untouched. Re-running `set` to tweak policy never requires
//     re-supplying the SA key.
//
//   - `test` defaults to the persisted policy + caller-supplied credentials
//     and hydrates the rest (agent_id, command, envs) from
//     GET /api/connections/{name}. --file overrides the policy when a draft
//     needs to be exercised before persistence. The credentials file is
//     always required because the gateway will not echo stored ciphertext
//     back, even for testing.
package admin

import (
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	fedFile               string
	fedCredentialsFile    string
	fedUserEmail          string
	fedUserID             string
	fedTestScript         string
	fedTestConnectionFlag string
)

func init() {
	MainCmd.AddCommand(federationCmd)
	federationCmd.AddCommand(federationGetCmd)
	federationCmd.AddCommand(federationSetCmd)
	federationCmd.AddCommand(federationDeleteCmd)
	federationCmd.AddCommand(federationTestCmd)

	federationCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", "",
		"Output format. One of: (json)")

	federationSetCmd.Flags().StringVar(&fedFile, "file", "",
		"Path to a YAML file with the federation policy (required)")
	federationSetCmd.Flags().StringVar(&fedCredentialsFile, "credentials-file", "",
		"Path to the admin credentials file (e.g. GCP SA JSON). "+
			"Omit on an update to preserve the stored value.")
	_ = federationSetCmd.MarkFlagRequired("file")

	federationTestCmd.Flags().StringVar(&fedTestConnectionFlag, "connection", "",
		"Name or UUID of the connection to test against (required)")
	federationTestCmd.Flags().StringVar(&fedFile, "file", "",
		"Path to a YAML file with a candidate federation policy. "+
			"If omitted, the persisted config for the connection is used.")
	federationTestCmd.Flags().StringVar(&fedCredentialsFile, "credentials-file", "",
		"Path to the admin credentials file. Required.")
	federationTestCmd.Flags().StringVar(&fedUserEmail, "user-email", "",
		"Synthetic user email to resolve against (required)")
	federationTestCmd.Flags().StringVar(&fedUserID, "user-id", "",
		"Synthetic user ID. Defaults to a deterministic UUID derived from --user-email.")
	federationTestCmd.Flags().StringVar(&fedTestScript, "test-script", "SELECT 1",
		"Smoke probe payload (fed to the agent process on stdin)")
	_ = federationTestCmd.MarkFlagRequired("connection")
	_ = federationTestCmd.MarkFlagRequired("user-email")
	_ = federationTestCmd.MarkFlagRequired("credentials-file")
}

var federationCmd = &cobra.Command{
	Use:     "federation",
	Aliases: []string{"fed"},
	Short:   "Manage IAM federation for connections",
	Long: `Manage per-connection IAM federation. Each subcommand operates on a
single connection identified by name or ID. Admin role is required.`,
}

type federationFile struct {
	BuiltinProvider         string         `yaml:"builtin_provider"`
	IdentitySourceAttribute string         `yaml:"identity_source_attribute"`
	IdentityTargetTemplate  string         `yaml:"identity_target_template"`
	FallbackPolicy          string         `yaml:"fallback_policy"`
	TokenTTLSeconds         int            `yaml:"token_ttl_seconds"`
	ExtraConfig             map[string]any `yaml:"extra_config"`
}

var federationGetCmd = &cobra.Command{
	Use:   "get CONNECTION",
	Short: "Get federation configuration for a connection",
	Example: `  hoop admin federation get my-bq
  hoop admin federation get my-bq -o json`,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			_ = cmd.Usage()
			styles.PrintErrorAndExit("missing connection name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		conn := args[0]
		config := clientconfig.GetClientConfigOrDie()
		decodeTo := "object"
		if outputFlag == "json" {
			decodeTo = "raw"
		}
		obj, _, err := httpRequest(&apiResource{
			suffixEndpoint: path.Join("/api/connections", conn, "federation"),
			conf:           config,
			decodeTo:       decodeTo,
		})
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		if outputFlag == "json" {
			raw, _ := obj.([]byte)
			fmt.Print(string(raw))
			return
		}
		m, _ := obj.(map[string]any)
		printFederationConfigTable(conn, m)
	},
}

var federationSetCmd = &cobra.Command{
	Use:   "set CONNECTION --file FILE [--credentials-file SA_FILE]",
	Short: "Create or update federation configuration for a connection",
	Example: `  # First-time setup
  hoop admin federation set my-bq --file federation.yaml --credentials-file sa.json

  # Update policy only, keep stored credentials
  hoop admin federation set my-bq --file federation.yaml`,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			_ = cmd.Usage()
			styles.PrintErrorAndExit("missing connection name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		conn := args[0]
		config := clientconfig.GetClientConfigOrDie()

		ff, err := parseFederationFile(fedFile)
		if err != nil {
			styles.PrintErrorAndExit("failed parsing %q: %v", fedFile, err)
		}
		body := federationBodyFromFile(ff)

		if fedCredentialsFile != "" {
			creds, err := os.ReadFile(fedCredentialsFile)
			if err != nil {
				styles.PrintErrorAndExit("failed reading credentials file %q: %v",
					fedCredentialsFile, err)
			}
			body["admin_credentials_json"] = string(creds)
		}

		decodeTo := "object"
		if outputFlag == "json" {
			decodeTo = "raw"
		}
		obj, err := httpBodyRequest(&apiResource{
			suffixEndpoint: path.Join("/api/connections", conn, "federation"),
			conf:           config,
			decodeTo:       decodeTo,
		}, "PUT", body)
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		if outputFlag == "json" {
			raw, _ := obj.([]byte)
			fmt.Print(string(raw))
			return
		}
		m, _ := obj.(map[string]any)
		fmt.Printf("federation configured for connection %q\n\n", conn)
		printFederationConfigTable(conn, m)
	},
}

var federationDeleteCmd = &cobra.Command{
	Use:   "delete CONNECTION",
	Short: "Delete federation configuration for a connection",
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			_ = cmd.Usage()
			styles.PrintErrorAndExit("missing connection name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		conn := args[0]
		config := clientconfig.GetClientConfigOrDie()
		if err := httpDeleteRequest(&apiResource{
			suffixEndpoint: path.Join("/api/connections", conn, "federation"),
			conf:           config,
		}); err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		fmt.Printf("federation removed for connection %q\n", conn)
	},
}

var federationTestCmd = &cobra.Command{
	Use:   "test --connection NAME --user-email EMAIL --credentials-file SA_FILE [--file FILE]",
	Short: "Dry-run federation end-to-end against an existing connection",
	Long: `Resolve a candidate federation policy against a synthetic user and
dispatch a one-shot probe (the --test-script payload) to the agent
associated with the named connection. No state is written.

The connection must already exist: the CLI fetches agent_id, command, and
envvar-typed secrets from it. filesystem-typed secrets are NOT forwarded
— the test endpoint cannot materialize files.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		conn := fedTestConnectionFlag
		config := clientconfig.GetClientConfigOrDie()

		// 1) Policy: --file overrides persisted config.
		var policyMap map[string]any
		if fedFile != "" {
			ff, err := parseFederationFile(fedFile)
			if err != nil {
				styles.PrintErrorAndExit("failed parsing %q: %v", fedFile, err)
			}
			policyMap = federationBodyFromFile(ff)
		} else {
			fed, _, err := httpRequest(&apiResource{
				suffixEndpoint: path.Join("/api/connections", conn, "federation"),
				conf:           config,
				decodeTo:       "object",
			})
			if err != nil {
				styles.PrintErrorAndExit(
					"failed loading persisted federation config for %q: %v "+
						"(pass --file to supply a draft policy instead)", conn, err)
			}
			policyMap, _ = fed.(map[string]any)
			// Strip server-only fields the test endpoint must not see.
			delete(policyMap, "id")
			delete(policyMap, "connection_id")
			delete(policyMap, "has_admin_credentials")
			delete(policyMap, "created_at")
			delete(policyMap, "updated_at")
		}

		// 2) Credentials are always supplied explicitly.
		credsBytes, err := os.ReadFile(fedCredentialsFile)
		if err != nil {
			styles.PrintErrorAndExit("failed reading credentials file %q: %v",
				fedCredentialsFile, err)
		}
		policyMap["admin_credentials_json"] = string(credsBytes)

		// 3) Hydrate the candidate connection from the persisted row.
		connObj, _, err := httpRequest(&apiResource{
			suffixEndpoint: path.Join("/api/connections", conn),
			conf:           config,
			decodeTo:       "object",
		})
		if err != nil {
			styles.PrintErrorAndExit("failed loading connection %q: %v", conn, err)
		}
		connMap, _ := connObj.(map[string]any)
		if connMap == nil {
			styles.PrintErrorAndExit("unexpected empty connection response")
		}
		agentID, _ := connMap["agent_id"].(string)
		if agentID == "" {
			styles.PrintErrorAndExit("connection %q has no agent_id", conn)
		}
		cmdList := toStringSlice(connMap["command"])
		if len(cmdList) == 0 {
			styles.PrintErrorAndExit("connection %q has an empty command", conn)
		}
		envs, skippedFS := extractTestEnvs(connMap["secret"])
		if len(skippedFS) > 0 {
			fmt.Fprintf(os.Stderr,
				"note: skipping filesystem-typed secrets the test endpoint cannot materialize: %s\n",
				strings.Join(skippedFS, ", "))
		}

		userID := fedUserID
		if userID == "" {
			userID = uuid.NewSHA1(uuid.NameSpaceURL,
				[]byte("federation-test:"+fedUserEmail)).String()
		}

		body := map[string]any{
			"user_email": fedUserEmail,
			"user_id":    userID,
			"config":     policyMap,
			"connection": map[string]any{
				"agent_id":    agentID,
				"type":        connMap["type"],
				"subtype":     connMap["subtype"],
				"command":     cmdList,
				"test_script": fedTestScript,
				"envs":        envs,
			},
		}

		decodeTo := "object"
		if outputFlag == "json" {
			decodeTo = "raw"
		}
		obj, err := httpBodyRequest(&apiResource{
			suffixEndpoint: "/api/federation/test",
			conf:           config,
			decodeTo:       decodeTo,
		}, "POST", body)
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		if outputFlag == "json" {
			raw, _ := obj.([]byte)
			fmt.Print(string(raw))
			return
		}
		m, _ := obj.(map[string]any)
		os.Exit(printFederationTestResult(conn, m))
	},
}

func parseFederationFile(filePath string) (*federationFile, error) {
	if filePath == "" {
		return nil, fmt.Errorf("file path is empty")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var ff federationFile
	if err := yaml.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("invalid YAML: %v", err)
	}
	if ff.BuiltinProvider == "" {
		return nil, fmt.Errorf("builtin_provider is required (e.g. gcp_iam)")
	}
	switch ff.FallbackPolicy {
	case "", "deny", "static":
	default:
		return nil, fmt.Errorf("fallback_policy must be one of: deny, static (got %q)",
			ff.FallbackPolicy)
	}
	return &ff, nil
}

func federationBodyFromFile(ff *federationFile) map[string]any {
	body := map[string]any{
		"hook_source":      "builtin",
		"builtin_provider": ff.BuiltinProvider,
	}
	if ff.IdentitySourceAttribute != "" {
		body["identity_source_attribute"] = ff.IdentitySourceAttribute
	}
	if ff.IdentityTargetTemplate != "" {
		body["identity_target_template"] = ff.IdentityTargetTemplate
	}
	if ff.FallbackPolicy != "" {
		body["fallback_policy"] = ff.FallbackPolicy
	}
	if ff.TokenTTLSeconds > 0 {
		body["token_ttl_seconds"] = ff.TokenTTLSeconds
	}
	if len(ff.ExtraConfig) > 0 {
		body["extra_config"] = ff.ExtraConfig
	}
	return body
}

// extractTestEnvs decodes the connection's wire-format secret map
// ({"envvar:NAME": base64-or-secretref}) into the plain NAME→plaintext
// form the test endpoint expects.
//
// envvar entries with valid base64 values are decoded. Entries with a
// secret-manager reference prefix (e.g. "_aws:...", "_envjson:...") are
// forwarded verbatim — they resolve on the agent at session-open and the
// test endpoint passes them straight through.
//
// filesystem entries are reported as skipped: the test endpoint uses
// BareExec which does not materialize the per-session /tmp files the
// agent's terminal pipeline would. A real session strips them anyway
// when superseded by federation (e.g. GOOGLE_APPLICATION_CREDENTIALS
// vs gcp_iam).
func extractTestEnvs(secretAny any) (map[string]string, []string) {
	plain := map[string]string{}
	var skipped []string
	secret, _ := secretAny.(map[string]any)
	for key, val := range secret {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		prefix, name := parts[0], parts[1]
		switch prefix {
		case "envvar":
			s, ok := val.(string)
			if !ok {
				continue
			}
			if strings.HasPrefix(s, "_") {
				plain[name] = s // secret-manager ref, resolved on the agent
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				plain[name] = s
				continue
			}
			plain[name] = string(decoded)
		case "filesystem":
			skipped = append(skipped, name)
		}
	}
	sort.Strings(skipped)
	return plain, skipped
}

func toStringSlice(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func printFederationConfigTable(conn string, m map[string]any) {
	if m == nil {
		fmt.Println("(empty response)")
		return
	}
	yesNo := func(v any) string {
		if b, ok := v.(bool); ok && b {
			return "yes"
		}
		return "no"
	}
	str := func(v any) string {
		if v == nil {
			return "-"
		}
		s := fmt.Sprintf("%v", v)
		if s == "" {
			return "-"
		}
		return s
	}
	extraConfig := "-"
	if ec, ok := m["extra_config"].(map[string]any); ok && len(ec) > 0 {
		keys := make([]string, 0, len(ec))
		for k := range ec {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, ec[k]))
		}
		extraConfig = strings.Join(parts, ", ")
	}
	fmt.Printf("Connection:             %s\n", conn)
	fmt.Printf("Hook Source:            %s\n", str(m["hook_source"]))
	fmt.Printf("Provider:               %s\n", str(m["builtin_provider"]))
	fmt.Printf("Identity Source:        %s\n", str(m["identity_source_attribute"]))
	fmt.Printf("Identity Template:      %s\n", str(m["identity_target_template"]))
	fmt.Printf("Fallback Policy:        %s\n", str(m["fallback_policy"]))
	fmt.Printf("Token TTL Seconds:      %s\n", str(m["token_ttl_seconds"]))
	fmt.Printf("Has Admin Credentials:  %s\n", yesNo(m["has_admin_credentials"]))
	fmt.Printf("Extra Config:           %s\n", extraConfig)
	fmt.Printf("Created At:             %s\n", str(m["created_at"]))
	fmt.Printf("Updated At:             %s\n", str(m["updated_at"]))
}

// printFederationTestResult renders the dry-run response. Returns the
// process exit code (0 on success, 1 on any failure) so the verb composes
// in CI gates.
func printFederationTestResult(conn string, m map[string]any) int {
	if m == nil {
		fmt.Println("(empty response)")
		return 1
	}
	success, _ := m["success"].(bool)
	str := func(v any) string {
		if v == nil {
			return "-"
		}
		s := fmt.Sprintf("%v", v)
		if s == "" {
			return "-"
		}
		return s
	}
	joinAny := func(v any) string {
		list, ok := v.([]any)
		if !ok || len(list) == 0 {
			return "-"
		}
		out := make([]string, 0, len(list))
		for _, item := range list {
			out = append(out, fmt.Sprintf("%v", item))
		}
		sort.Strings(out)
		return strings.Join(out, ", ")
	}

	status := "FAILED"
	if success {
		status = "SUCCESS"
	}

	fmt.Printf("Connection:             %s\n", conn)
	fmt.Printf("Status:                 %s\n", status)
	fmt.Printf("Resolved Principal:     %s\n", str(m["resolved_principal"]))
	fmt.Printf("Admin Principal:        %s\n", str(m["admin_principal"]))
	fmt.Printf("Token Expires At:       %s\n", str(m["token_expires_at"]))
	fmt.Printf("Injected Env Var Keys:  %s\n", joinAny(m["env_var_keys"]))
	fmt.Printf("Superseded Static Envs: %s\n", joinAny(m["superseded_env_vars"]))
	fmt.Printf("Probe Status:           %s\n", str(m["probe_status"]))
	if errStr, _ := m["error"].(string); errStr != "" {
		fmt.Printf("Error:                  %s\n", errStr)
	}
	if out, _ := m["probe_output"].(string); out != "" {
		fmt.Println()
		fmt.Println("Probe Output:")
		fmt.Println(out)
	}
	if success {
		return 0
	}
	return 1
}
