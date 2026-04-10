package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

// knownSessionFields is the ordered list of fields users can select via --fields.
var knownSessionFields = []string{
	"id", "user", "role", "type", "start_date", "end_date", "status",
}

const defaultSessionFields = "id,user,role,type,start_date,status"

var sessionsFlags struct {
	user           string
	role           string
	connType       string
	reviewApprover string
	reviewStatus   string
	jiraIssueKey   string
	startDate      string
	endDate        string
	limit          int
	offset         int
	fields         string
	jsonOutput     bool
	quietOutput    bool
}

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Manage sessions",
	Long:  "List and inspect sessions.",
}

var sessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sessions",
	Long:  "List sessions you have access to, with optional filters for connection, type, review status, and date range.",
	Example: `  # List sessions (default fields: id, user, role, type, start_date, status)
  hoop sessions list

  # Show only specific fields
  hoop sessions list --fields id,user,role,type

  # Filter by role
  hoop sessions list --role my-db

  # Pretty-printed JSON (human readable)
  hoop sessions list --json

  # Raw compact JSON (for scripting)
  hoop sessions list --quiet

  # Filter by type and date range (RFC3339)
  hoop sessions list --type postgres --start-date 2024-01-01T00:00:00Z --end-date 2024-01-31T23:59:59Z

  # Paginate results
  hoop sessions list --limit 20 --offset 40

  # Find sessions for a specific role and pipe to jq
  hoop sessions list --role prod-db --quiet | jq '.data[].id'

  # List sessions with a review pending approval
  hoop sessions list --review-status pending`,
	Run: func(cmd *cobra.Command, args []string) {
		runSessions()
	},
}

func init() {
	f := sessionsListCmd.Flags()

	// Filter flags
	f.StringVar(&sessionsFlags.user, "user", "", "Filter by user subject ID")
	f.StringVar(&sessionsFlags.role, "role", "", "Filter by role name")
	f.StringVar(&sessionsFlags.connType, "type", "", "Filter by connection type")
	f.StringVar(&sessionsFlags.reviewApprover, "review-approver", "", "Filter by review approver email")
	f.StringVar(&sessionsFlags.reviewStatus, "review-status", "", "Filter by review status")
	f.StringVar(&sessionsFlags.jiraIssueKey, "jira-issue-key", "", "Filter by Jira issue key")
	f.StringVar(&sessionsFlags.startDate, "start-date", "", "Filter from date (RFC3339, e.g. 2024-01-01T00:00:00Z)")
	f.StringVar(&sessionsFlags.endDate, "end-date", "", "Filter until date (RFC3339, e.g. 2024-12-31T23:59:59Z)")
	f.IntVar(&sessionsFlags.limit, "limit", 0, "Maximum number of records to return (max 100)")
	f.IntVar(&sessionsFlags.offset, "offset", 0, "Pagination offset")

	// Output flags
	f.StringVar(&sessionsFlags.fields, "fields", defaultSessionFields,
		fmt.Sprintf("Comma-separated fields to display. Available: %s", strings.Join(knownSessionFields, ", ")))
	f.BoolVar(&sessionsFlags.jsonOutput, "json", false, "Output as formatted JSON (human-readable)")
	f.BoolVar(&sessionsFlags.quietOutput, "quiet", false, "Output as raw compact JSON (for scripting)")

	sessionsCmd.AddCommand(sessionsListCmd)
	sessionsCmd.AddCommand(sessionsGetCmd)
	rootCmd.AddCommand(sessionsCmd)
}

var sessionsGetFlags struct {
	jsonOutput  bool
	quietOutput bool
}

var sessionsGetCmd = &cobra.Command{
	Use:   "get <session-id>",
	Short: "Get a session by ID",
	Long:  "Fetch and display detailed information for a single session.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runSessionGet(args[0])
	},
}

func init() {
	f := sessionsGetCmd.Flags()
	f.BoolVar(&sessionsGetFlags.jsonOutput, "json", false, "Output as formatted JSON (human-readable)")
	f.BoolVar(&sessionsGetFlags.quietOutput, "quiet", false, "Output as raw compact JSON (for scripting)")
}

type sessionsResponse struct {
	Data        []map[string]any `json:"data"`
	HasNextPage bool             `json:"has_next_page"`
	Total       int              `json:"total"`
}

func runSessions() {
	config := clientconfig.GetClientConfigOrDie()

	resp, err := fetchSessions(config)
	if err != nil {
		styles.PrintErrorAndExit("Failed to fetch sessions: %v", err)
	}

	if sessionsFlags.quietOutput {
		data, err := json.Marshal(resp)
		if err != nil {
			styles.PrintErrorAndExit("Failed to encode JSON: %v", err)
		}
		fmt.Println(string(data))
		return
	}

	if sessionsFlags.jsonOutput {
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			styles.PrintErrorAndExit("Failed to encode JSON: %v", err)
		}
		fmt.Println(string(data))
		return
	}

	displaySessions(resp)
}

func fetchSessions(config *clientconfig.Config) (*sessionsResponse, error) {
	params := url.Values{}
	if sessionsFlags.user != "" {
		params.Set("user", sessionsFlags.user)
	}
	if sessionsFlags.role != "" {
		params.Set("connection", sessionsFlags.role)
	}
	if sessionsFlags.connType != "" {
		params.Set("type", sessionsFlags.connType)
	}
	if sessionsFlags.reviewApprover != "" {
		params.Set("review.approver", sessionsFlags.reviewApprover)
	}
	if sessionsFlags.reviewStatus != "" {
		params.Set("review.status", sessionsFlags.reviewStatus)
	}
	if sessionsFlags.jiraIssueKey != "" {
		params.Set("jira_issue_key", sessionsFlags.jiraIssueKey)
	}
	if sessionsFlags.startDate != "" {
		params.Set("start_date", sessionsFlags.startDate)
	}
	if sessionsFlags.endDate != "" {
		params.Set("end_date", sessionsFlags.endDate)
	}
	if sessionsFlags.limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", sessionsFlags.limit))
	}
	if sessionsFlags.offset > 0 {
		params.Set("offset", fmt.Sprintf("%d", sessionsFlags.offset))
	}

	rawURL := config.ApiURL + "/api/sessions"
	if len(params) > 0 {
		rawURL = rawURL + "?" + params.Encode()
	}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	httpResp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed performing request: %v", err)
	}
	defer httpResp.Body.Close()

	log.Debugf("sessions http response %v", httpResp.StatusCode)

	if httpResp.StatusCode != 200 {
		body, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("failed performing request, status=%v, body=%v", httpResp.StatusCode, string(body))
	}

	var result sessionsResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed decoding response: %v", err)
	}

	return &result, nil
}

// setSessionAuthHeaders sets auth and user-agent headers on a request.
func setSessionAuthHeaders(req *http.Request, config *clientconfig.Config) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%v", version.Get().Version))
}

func displaySessions(resp *sessionsResponse) {
	if len(resp.Data) == 0 {
		fmt.Println("No sessions found")
		return
	}

	selectedFields := parseSessionFields(sessionsFlags.fields)

	var headers []string
	for _, f := range selectedFields {
		headers = append(headers, strings.ToUpper(f))
	}

	var rows [][]string
	for _, s := range resp.Data {
		var cols []string
		for _, f := range selectedFields {
			cols = append(cols, extractSessionField(s, f))
		}
		rows = append(rows, cols)
	}

	fmt.Println(styles.RenderTable(headers, rows))

	if resp.HasNextPage {
		nextOffset := sessionsFlags.offset + len(resp.Data)
		fmt.Fprintf(os.Stderr, "%d of %d shown. Use --offset %d to see the next page.\n",
			len(resp.Data), resp.Total, nextOffset)
	}

	fmt.Fprintln(os.Stderr, "\nTry also:")
	fmt.Fprintln(os.Stderr, "  hoop sessions get <id>                               # inspect a specific session")
	fmt.Fprintln(os.Stderr, "  hoop sessions get <id> --json                        # full detail as JSON")
	fmt.Fprintln(os.Stderr, "  hoop sessions list --role <name>                     # filter by role")
	fmt.Fprintln(os.Stderr, "  hoop sessions list --review-status pending           # sessions pending approval")
	fmt.Fprintln(os.Stderr, "  hoop sessions list --quiet | jq '.data[].id'         # pipe IDs to jq")
}

// parseSessionFields validates and deduplicates the comma-separated --fields value.
// Unknown fields are warned about and dropped; order is preserved.
func parseSessionFields(raw string) []string {
	known := make(map[string]bool, len(knownSessionFields))
	for _, f := range knownSessionFields {
		known[f] = true
	}

	var result []string
	seen := map[string]bool{}
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		if !known[f] {
			fmt.Fprintf(os.Stderr, "warning: unknown field %q ignored. Available: %s\n",
				f, strings.Join(knownSessionFields, ", "))
			continue
		}
		seen[f] = true
		result = append(result, f)
	}

	if len(result) == 0 {
		return parseSessionFields(defaultSessionFields)
	}
	return result
}

// extractSessionField returns the display value for a given field of a session.
// The input and output fields are truncated to keep the table readable.
func extractSessionField(s map[string]any, field string) string {
	switch field {
	case "user":
		return sessionDisplayUser(s)
	case "role":
		return toStr(s["connection"])
	case "start_date", "end_date":
		return formatSessionDate(toStr(s[field]))
	case "status":
		return sessionStatus(s)
	default:
		return toStr(s[field])
	}
}

// sessionDisplayUser returns the user email from the session, falling back to
// the subject ID if the email is absent.
func sessionDisplayUser(s map[string]any) string {
	if email := toStr(s["user"]); email != "-" {
		return email
	}
	return toStr(s["user_id"])
}

// sessionStatus returns the review status when present, otherwise derives it
// from the presence of an end_date.
func sessionStatus(s map[string]any) string {
	if review, ok := s["review"].(map[string]any); ok {
		if status := toStr(review["status"]); status != "-" {
			return status
		}
	}
	if endDate := toStr(s["end_date"]); endDate != "-" && endDate != "" {
		return "ended"
	}
	return "active"
}

// formatSessionDate trims sub-second precision from RFC3339 timestamps.
func formatSessionDate(s string) string {
	if s == "-" || s == "" {
		return "-"
	}
	if len(s) > 19 {
		return s[:19]
	}
	return s
}

// --- sessions get ---

const sessionGetInputTruncLen = 200

func runSessionGet(id string) {
	config := clientconfig.GetClientConfigOrDie()

	session, err := fetchSessionByID(config, id)
	if err != nil {
		styles.PrintErrorAndExit("Failed to fetch session: %v", err)
	}

	if sessionsGetFlags.quietOutput {
		data, err := json.Marshal(session)
		if err != nil {
			styles.PrintErrorAndExit("Failed to encode JSON: %v", err)
		}
		fmt.Println(string(data))
		return
	}

	if sessionsGetFlags.jsonOutput {
		data, err := json.MarshalIndent(session, "", "  ")
		if err != nil {
			styles.PrintErrorAndExit("Failed to encode JSON: %v", err)
		}
		fmt.Println(string(data))
		return
	}

	displaySession(session)
}

func fetchSessionByID(config *clientconfig.Config, id string) (map[string]any, error) {
	req, err := http.NewRequest("GET", config.ApiURL+"/api/sessions/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %v", err)
	}
	setSessionAuthHeaders(req, config)

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed performing request: %v", err)
	}
	defer resp.Body.Close()

	log.Debugf("session get http response %v", resp.StatusCode)

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status=%v, body=%v", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed decoding response: %v", err)
	}
	return result, nil
}

func displaySession(s map[string]any) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	defer w.Flush()

	kv := func(label, value string) {
		fmt.Fprintf(w, "  %-14s\t%s\n", label, value)
	}
	sep := func() { fmt.Fprintln(w) }

	// Resolve user: prefer user_name or user (email), fall back to user_id.
	user := toStr(s["user"])
	if name := toStr(s["user_name"]); name != "-" {
		user = name
	}

	role := toStr(s["connection"])
	resource := toStr(s["resource_name"])
	connType := toStr(s["type"])
	subtype := toStr(s["connection_subtype"])
	verb := toStr(s["verb"])
	status := toStr(s["status"])
	started := formatSessionDate(toStr(s["start_date"]))
	ended := formatSessionDate(toStr(s["end_date"]))
	exitCode := toStr(s["exit_code"])

	kv("ID", toStr(s["id"]))
	kv("User", user)
	kv("Role", role)
	if resource != "-" {
		kv("Resource", resource)
	}
	kv("Type", connType)
	if subtype != "-" {
		kv("Subtype", subtype)
	}
	if verb != "-" {
		kv("Verb", verb)
	}
	kv("Status", status)
	kv("Started", started)
	kv("Ended", ended)
	if exitCode != "-" && exitCode != "0" {
		kv("Exit Code", exitCode)
	}

	// Input (truncated).
	if script, ok := s["script"].(map[string]any); ok {
		if data := toStr(script["data"]); data != "-" {
			sep()
			fmt.Fprintln(w, "  Input")
			runes := []rune(data)
			truncated := string(runes)
			suffix := ""
			if len(runes) > sessionGetInputTruncLen {
				truncated = string(runes[:sessionGetInputTruncLen])
				suffix = "…"
			}
			// Indent each line of the input.
			for _, line := range strings.Split(truncated, "\n") {
				fmt.Fprintf(w, "    %s\n", line)
			}
			if suffix != "" {
				fmt.Fprintln(w, "    …  (use --json to see full input)")
			}
		}
	}

	// Review section.
	if review, ok := s["review"].(map[string]any); ok {
		sep()
		fmt.Fprintln(w, "  Review")
		reviewStatus := toStr(review["status"])
		reviewType := toStr(review["type"])
		minApprovals := toStr(review["min_approvals"])

		line := fmt.Sprintf("    Status: %-12s  Type: %s", reviewStatus, reviewType)
		if minApprovals != "-" && minApprovals != "0" {
			line += fmt.Sprintf("  Min approvals: %s", minApprovals)
		}
		fmt.Fprintln(w, line)

		if groups, ok := review["review_groups_data"].([]any); ok && len(groups) > 0 {
			fmt.Fprintln(w, "    Groups:")
			for _, g := range groups {
				gm, ok := g.(map[string]any)
				if !ok {
					continue
				}
				grp := toStr(gm["group"])
				gStatus := toStr(gm["status"])
				reviewedBy := "-"
				reviewDate := "-"
				if rb, ok := gm["reviewed_by"].(map[string]any); ok {
					if email := toStr(rb["email"]); email != "-" {
						reviewedBy = email
					} else if name := toStr(rb["name"]); name != "-" {
						reviewedBy = name
					}
				}
				if rd := toStr(gm["review_date"]); rd != "-" {
					reviewDate = formatSessionDate(rd)
				}
				if reviewedBy != "-" {
					fmt.Fprintf(w, "      %-20s  %-10s  by %s on %s\n", grp, gStatus, reviewedBy, reviewDate)
				} else {
					fmt.Fprintf(w, "      %-20s  %s\n", grp, gStatus)
				}
			}
		}
	}

	// AI analysis section.
	if ai, ok := s["ai_analysis"].(map[string]any); ok {
		sep()
		fmt.Fprintln(w, "  AI Analysis")
		kv("  Risk", toStr(ai["risk_level"]))
		kv("  Action", toStr(ai["action"]))
		if title := toStr(ai["title"]); title != "-" {
			kv("  Title", title)
		}
		if explanation := toStr(ai["explanation"]); explanation != "-" {
			kv("  Details", explanation)
		}
	}

	sep()
}
