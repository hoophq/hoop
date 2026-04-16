package runbooks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

// ---- flags ----

var (
	parametersFlag []string
	noColorFlag    bool
)

var runbooksListFlags struct {
	role        string
	jsonOutput  bool
	quietOutput bool
}

var runbooksCreateFlags struct {
	path          string
	content       string
	contentFile   string
	repo          string
	commitMessage string
	overwrite     bool
	jsonOutput    bool
	quietOutput   bool
}

// ---- root command ----

var MainCmd = &cobra.Command{
	Use:          "runbooks",
	Short:        "Manage runbooks",
	Long:         "List, create, and validate runbooks stored in connected git repositories.",
	Aliases:      []string{"runbook"},
	Hidden:       false,
	SilenceUsage: false,
}

// ---- lint ----

var exampleDesc = `hoop runbooks lint ./runbooks/myfile.runbook.py
hoop runbooks lint /root/another-file.runbook.py
hoop runbooks lint -p account_id=123 <<< '{{ .account_id | description "Account ID" }}'
`

var lintCmd = &cobra.Command{
	Use:          "lint (PATH | STDIN)",
	Short:        "Validate runbook files and provide error messages when syntax errors are detected.",
	SilenceUsage: false,
	Example:      exampleDesc,
	Run: func(cmd *cobra.Command, args []string) {
		info, err := os.Stdin.Stat()
		if err != nil {
			printErr("%s", err.Error())
		}
		var content []byte
		isStdinInput := info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0
		if isStdinInput {
			stdinPipe := os.NewFile(uintptr(syscall.Stdin), "/dev/stdin")
			reader := bufio.NewReader(stdinPipe)
			for {
				stdinInput, err := reader.ReadByte()
				if err != nil && err == io.EOF {
					break
				}
				content = append(content, stdinInput)
			}
			stdinPipe.Close()
		} else {
			if len(args) == 0 || len(args[0]) == 0 {
				printErr("missing file path or stdin containing the file contents")
			}
			content, err = os.ReadFile(args[0])
			if err != nil {
				printErr("unable to read file %v, reason=%v", args[0], err)
			}
		}
		tmpl, err := runbooks.Parse(string(content))
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed parsing runbook template, reason=%v\n", err)
			printErrContext(content, err)
			os.Exit(1)
		}

		// add a default value to input parameters if the attribute is required
		// and it contains a default value. The template engine requires the key attribute
		// is set to not fail
		inputParams := parseParameters()
		for key, config := range tmpl.Attributes() {
			if configParams, ok := config.(map[string]any); ok {
				var isRequired bool
				if requiredObj, ok := configParams["required"]; ok {
					isRequired, _ = requiredObj.(bool)
				}
				if defaultObj, ok := configParams["default"]; ok {
					defaultVal, _ := defaultObj.(string)
					if isRequired {
						if _, ok := inputParams[key]; !ok {
							inputParams[key] = defaultVal
						}
					}
				}
			}
		}

		out := bytes.NewBuffer([]byte{})
		if err := tmpl.Execute(out, inputParams); err != nil {
			fmt.Fprintf(os.Stderr, "failed parsing template parameters, reason=%v\n", err)
			printErrContext(content, err)
			os.Exit(1)
		}
		fmt.Println(out.String())
	},
}

// ---- list ----

var runbooksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List runbooks",
	Long:  "List all runbooks available across configured git repositories.",
	Example: `  # List all runbooks
  hoop runbooks list

  # Filter by role name
  hoop runbooks list --role my-db

  # Output as JSON
  hoop runbooks list --json`,
	Run: func(cmd *cobra.Command, args []string) {
		runRunbooksList()
	},
}

// ---- create ----

var runbooksCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a runbook file",
	Long:  "Commit a new runbook file to the configured git repository.",
	Example: `  # From a local file
  hoop runbooks create --path ops/restart.runbook.sh --content-file ./restart.sh

  # From stdin
  hoop runbooks create --path ops/restart.runbook.sh < ./restart.sh

  # Inline content
  hoop runbooks create --path ops/restart.runbook.sh --content "#!/bin/bash\necho restarting..."

  # Specify the repository when multiple are configured
  hoop runbooks create --path ops/restart.runbook.sh --content-file ./restart.sh --repo https://github.com/myorg/myrepo

  # Overwrite an existing file
  hoop runbooks create --path ops/restart.runbook.sh --content-file ./restart.sh --overwrite

  # Custom commit message
  hoop runbooks create --path ops/restart.runbook.sh --content-file ./restart.sh --commit-message "feat: add restart runbook"`,
	Run: func(cmd *cobra.Command, args []string) {
		runRunbooksCreate()
	},
}

// ---- init ----

func init() {
	lintCmd.Flags().StringSliceVarP(&parametersFlag, "parameter", "p", nil, "The parameter to use when parsing the runbook")
	lintCmd.Flags().BoolVar(&noColorFlag, "no-color", false, "Omit color in output")

	fl := runbooksListCmd.Flags()
	fl.StringVar(&runbooksListFlags.role, "role", "", "Filter runbooks by role name")
	fl.BoolVar(&runbooksListFlags.jsonOutput, "json", false, "Output as formatted JSON")
	fl.BoolVar(&runbooksListFlags.quietOutput, "quiet", false, "Output as compact JSON (for scripting)")

	fc := runbooksCreateCmd.Flags()
	fc.StringVar(&runbooksCreateFlags.path, "path", "", "File path relative to the repository root, e.g. ops/restart.runbook.sh (required)")
	fc.StringVar(&runbooksCreateFlags.content, "content", "", "Inline content of the runbook file")
	fc.StringVar(&runbooksCreateFlags.contentFile, "content-file", "", "Path to a local file whose content will be used")
	fc.StringVar(&runbooksCreateFlags.repo, "repo", "", "Git repository URL when multiple repositories are configured")
	fc.StringVar(&runbooksCreateFlags.commitMessage, "commit-message", "", "Commit message (defaults to \"feat: add <path>\")")
	fc.BoolVar(&runbooksCreateFlags.overwrite, "overwrite", false, "Overwrite the file if it already exists")
	fc.BoolVar(&runbooksCreateFlags.jsonOutput, "json", false, "Output result as formatted JSON")
	fc.BoolVar(&runbooksCreateFlags.quietOutput, "quiet", false, "Output result as compact JSON (for scripting)")
	_ = runbooksCreateCmd.MarkFlagRequired("path")

	MainCmd.AddCommand(lintCmd)
	MainCmd.AddCommand(runbooksListCmd)
	MainCmd.AddCommand(runbooksCreateCmd)
}

// ---- list implementation ----

type runbookItem struct {
	Name string `json:"name"`
}

type runbookRepository struct {
	Repository    string        `json:"repository"`
	Commit        string        `json:"commit"`
	CommitAuthor  string        `json:"commit_author"`
	CommitMessage string        `json:"commit_message"`
	Items         []runbookItem `json:"items"`
}

type runbooksListResponse struct {
	Errors       []string            `json:"errors"`
	Repositories []runbookRepository `json:"repositories"`
}

func runRunbooksList() {
	config := clientconfig.GetClientConfigOrDie()

	rawURL := config.ApiURL + "/api/runbooks"
	if runbooksListFlags.role != "" {
		rawURL += "?connection_name=" + runbooksListFlags.role
	}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		styles.PrintErrorAndExit("Failed to create request: %v", err)
	}
	setAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		styles.PrintErrorAndExit("Failed to fetch runbooks: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("runbooks list http response %v", httpResp.StatusCode)

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode != 200 {
		styles.PrintErrorAndExit("Failed to fetch runbooks, status=%v, body=%v", httpResp.StatusCode, string(body))
	}

	var resp runbooksListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}

	if runbooksListFlags.quietOutput {
		fmt.Println(string(body))
		return
	}
	if runbooksListFlags.jsonOutput {
		out, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(out))
		return
	}

	displayRunbooksList(&resp)
}

func displayRunbooksList(resp *runbooksListResponse) {
	for _, e := range resp.Errors {
		fmt.Printf("warning: %s\n", e)
	}

	var total int
	for _, r := range resp.Repositories {
		total += len(r.Items)
	}

	if total == 0 {
		fmt.Println("No runbooks found")
		return
	}

	var rows [][]string
	for _, repo := range resp.Repositories {
		for _, item := range repo.Items {
			rows = append(rows, []string{item.Name, repo.Repository})
		}
	}
	fmt.Println(styles.RenderTable([]string{"NAME", "REPOSITORY"}, rows))
}

// ---- create implementation ----

type runbookConfigRepo struct {
	Repository string `json:"repository"`
	GitUrl     string `json:"git_url"`
}

type runbookConfigResponse struct {
	Repositories []runbookConfigRepo `json:"repositories"`
}

func runRunbooksCreate() {
	config := clientconfig.GetClientConfigOrDie()

	content, err := resolveRunbookContent()
	if err != nil {
		styles.PrintErrorAndExit("%v", err)
	}

	configID, err := resolveRunbookConfigID(config)
	if err != nil {
		styles.PrintErrorAndExit("%v", err)
	}

	payload := map[string]any{
		"path":      runbooksCreateFlags.path,
		"content":   content,
		"overwrite": runbooksCreateFlags.overwrite,
	}
	if runbooksCreateFlags.commitMessage != "" {
		payload["commit_message"] = runbooksCreateFlags.commitMessage
	}

	body, err := json.Marshal(payload)
	if err != nil {
		styles.PrintErrorAndExit("Failed to encode request: %v", err)
	}

	req, err := http.NewRequest("POST", config.ApiURL+"/api/runbooks/configurations/"+configID+"/files", bytes.NewReader(body))
	if err != nil {
		styles.PrintErrorAndExit("Failed to create request: %v", err)
	}
	setAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		styles.PrintErrorAndExit("Failed to create runbook: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("runbooks create http response %v", httpResp.StatusCode)

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		styles.PrintErrorAndExit("Failed to read response: %v", err)
	}

	if httpResp.StatusCode == 409 {
		styles.PrintErrorAndExit("File already exists. Use --overwrite to replace it.")
	}
	if httpResp.StatusCode != 201 {
		styles.PrintErrorAndExit("Failed to create runbook, status=%v, body=%v", httpResp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		styles.PrintErrorAndExit("Failed to decode response: %v", err)
	}

	if runbooksCreateFlags.quietOutput {
		fmt.Println(string(respBody))
		return
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}

func resolveRunbookContent() (string, error) {
	if runbooksCreateFlags.content != "" {
		return runbooksCreateFlags.content, nil
	}

	if runbooksCreateFlags.contentFile != "" {
		data, err := os.ReadFile(runbooksCreateFlags.contentFile)
		if err != nil {
			return "", fmt.Errorf("failed reading content file: %v", err)
		}
		return string(data), nil
	}

	stat, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("failed checking stdin: %v", err)
	}
	if stat.Mode()&os.ModeCharDevice == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed reading stdin: %v", err)
		}
		if len(data) > 0 {
			return string(data), nil
		}
	}

	return "", fmt.Errorf("no content provided: use --content, --content-file, or pipe content via stdin")
}

func resolveRunbookConfigID(config *clientconfig.Config) (string, error) {
	req, err := http.NewRequest("GET", config.ApiURL+"/api/runbooks/configurations", nil)
	if err != nil {
		return "", fmt.Errorf("failed creating request: %v", err)
	}
	setAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		return "", fmt.Errorf("failed fetching runbook configurations: %v", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed reading response: %v", err)
	}

	if httpResp.StatusCode == 404 {
		return "", fmt.Errorf("no runbook configuration found — set one up in the hoop dashboard first")
	}
	if httpResp.StatusCode != 200 {
		return "", fmt.Errorf("failed fetching runbook configurations, status=%v, body=%v", httpResp.StatusCode, string(body))
	}

	var cfg runbookConfigResponse
	if err := json.Unmarshal(body, &cfg); err != nil {
		return "", fmt.Errorf("failed decoding runbook configurations: %v", err)
	}

	if len(cfg.Repositories) == 0 {
		return "", fmt.Errorf("no repositories configured")
	}

	if runbooksCreateFlags.repo == "" {
		if len(cfg.Repositories) > 1 {
			var urls []string
			for _, r := range cfg.Repositories {
				urls = append(urls, "  "+r.GitUrl)
			}
			return "", fmt.Errorf("multiple repositories configured, use --repo to specify one:\n%s", strings.Join(urls, "\n"))
		}
		return repoConfigID(cfg.Repositories[0].GitUrl), nil
	}

	for _, r := range cfg.Repositories {
		if r.GitUrl == runbooksCreateFlags.repo || r.Repository == runbooksCreateFlags.repo {
			return repoConfigID(r.GitUrl), nil
		}
	}

	return "", fmt.Errorf("repository %q not found in configured repositories", runbooksCreateFlags.repo)
}

func repoConfigID(gitURL string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(gitURL)).String()
}

// ---- helpers ----

func setAuthHeaders(req *http.Request, config *clientconfig.Config) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%v", version.Get().Version))
}

func parseParameters() map[string]string {
	out := map[string]string{}
	for _, parameter := range parametersFlag {
		key, val, found := strings.Cut(parameter, "=")
		if !found {
			continue
		}
		out[key] = val
	}
	return out
}

func printErrContext(content []byte, err error) {
	errMsg := err.Error()
	if strings.HasPrefix(errMsg, "template: :") {
		no, _, _ := strings.Cut(errMsg[11:], ":")
		lineNumber, err := strconv.Atoi(strings.TrimSpace(no))
		if err == nil {
			i := 0
			var contents []string
			for _, line := range bytes.Split(content, []byte("\n")) {
				i++
				newLine := string(line)
				if lineNumber == i && !noColorFlag {
					newLine = styles.KeywordHighlight(string(line))
				}
				contents = append(contents, newLine)
			}

			beforeContext := contents[max(lineNumber-3, 0):lineNumber]
			afterContext := contents[lineNumber:min(lineNumber+3, len(contents))]
			contentContext := strings.Join(beforeContext, "\n") + "\n" + strings.Join(afterContext, "\n")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, contentContext)
		}
	}
}

func printErr(format string, v ...any) {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(format, v...))
	os.Exit(1)
}
