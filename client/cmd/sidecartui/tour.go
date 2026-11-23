package sidecartui

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// The "Try it" section: when the sidecar fronts the demo API, the dashboard
// opens on a guided list of requests. Each one runs from here (enter), opens
// in a browser (o), or is copied as curl (c); its answer is shown with what
// the sidecar did to it. A request sent from another terminal ticks its step
// too, from the audit trail, so the guide follows whatever the person does.

// DemoOptions turns the Try it section on.
type DemoOptions struct {
	// OpenURL opens a URL in the person's browser. Nil hides the key.
	OpenURL func(string) error
	// Targets are the demos the config runs, each with its own steps. The
	// setup screens write both, so one tour shows every step.
	Targets []DemoTarget
	// Ports, Kind and Analyzer describe a single demo when Targets is
	// empty: the zero values mean the HTTP demo on its default ports.
	Ports    sidecardemo.Ports
	Kind     sidecardemo.Kind
	Analyzer bool
}

// DemoTarget is one demo the tour runs steps against.
type DemoTarget struct {
	// Kind is HTTP or Postgres; Ports where its listener and upstream are.
	Kind  sidecardemo.Kind
	Ports sidecardemo.Ports
	// Listener is the listener in front of it. An audit event ticks only
	// this demo's steps when it names that listener, so a masked API reply
	// does not tick the database's masking step. "" matches any listener.
	Listener string
	// Analyzer reports that the listener has an AI analyzer, which adds
	// the step that shows it.
	Analyzer bool
}

// label names a demo in the step list.
func (d DemoTarget) label() string {
	if d.Kind == sidecardemo.KindPostgres {
		return "Postgres · hoop's demo database"
	}
	return "HTTP · an invented API"
}

// analyzerTimeout bounds a step the model judges: one classification may take
// the analyzer's own 10 s default, then the request runs.
const analyzerTimeout = 30 * time.Second

// verdict is what the analyzer recorded for the step that shows it, read from
// the audit trail: the step's own answer is a 403 or a 201 and says nothing
// of the rating behind it.
type verdict struct {
	level, action, status, message string
}

type tourResult struct {
	step   int
	status int
	body   string
	dur    time.Duration
	err    error
}

type tourResultMsg tourResult

type tour struct {
	targets []DemoTarget
	steps   []sidecardemo.Step
	// at is the index in targets of each step's demo.
	at      []int
	cur     int
	done    []bool
	running int
	result  *tourResult
	open    func(string) error
	flash   string
	// lastOp is each session's last recorded operation, which a masked
	// event (it names none) is matched by.
	lastOp map[string]string
	// verdict is the analyzer's record of the last analyzer step run here,
	// nil until its audit event arrives.
	verdict *verdict
	// awaiting is the analyzer step whose verdict is wanted, -1 for none.
	awaiting int
}

func newTour(o *DemoOptions) *tour {
	targets := o.Targets
	if len(targets) == 0 {
		targets = []DemoTarget{{Kind: o.Kind, Ports: o.Ports, Analyzer: o.Analyzer}}
	}
	t := &tour{open: o.OpenURL, running: -1, awaiting: -1}
	for _, d := range targets {
		if d.Kind == "" {
			d.Kind = sidecardemo.KindHTTP
		}
		if d.Ports.Listen == "" || d.Ports.API == "" {
			d.Ports = sidecardemo.DefaultPortsFor(d.Kind)
		}
		t.targets = append(t.targets, d)
		for _, s := range sidecardemo.StepsFor(d.Kind, d.Analyzer) {
			t.steps = append(t.steps, s)
			t.at = append(t.at, len(t.targets)-1)
		}
	}
	t.done = make([]bool, len(t.steps))
	return t
}

// target is the demo step i runs against.
func (t *tour) target(i int) DemoTarget { return t.targets[t.at[i]] }

// pgAt reports whether step i is a Postgres step.
func (t *tour) pgAt(i int) bool { return t.target(i).Kind == sidecardemo.KindPostgres }

// pg reports whether the selected step is a Postgres step.
func (t *tour) pg() bool { return t.pgAt(t.cur) }

// command is step i as a command to paste.
func (t *tour) command(i int) string {
	d := t.target(i)
	return t.steps[i].Command(d.Kind, d.Ports)
}

// see ticks the step an audit event shows happened.
//
// A masked event names no operation: it is the response's, and the request
// it answers was recorded just before it on the same session. Its operation
// is that statement's, so the POST step's masked reply does not pass for
// the GET that "See masking" asks for.
//
// Only a statement the client sent sets that operation. The Postgres lane also
// records the server's CommandComplete as a statement ("SELECT 4", operation
// unknown) between the query and its masked rows; letting it count would leave
// the masked event matched to "unknown" and "See masking" never ticked.
func (t *tour) see(ev audit.Event) {
	op := string(ev.Operation)
	if (ev.Kind == audit.KindStatement || ev.Kind == "violation") && ev.Direction != inspect.FromServer {
		if t.lastOp == nil {
			t.lastOp = map[string]string{}
		}
		t.lastOp[string(ev.SessionID)] = op
	}
	if op == "" {
		op = t.lastOp[string(ev.SessionID)]
	}
	level, status := ev.Metadata[metaRiskLevel], ev.Metadata[metaAIStatus]
	if t.awaiting >= 0 && (level != "" || status != "") && ev.Direction != inspect.FromServer &&
		t.from(t.awaiting, ev) && strings.EqualFold(op, t.steps[t.awaiting].SeenOp) {
		t.verdict = &verdict{level: level, action: ev.Metadata[metaRiskAction], status: status, message: ev.Message}
	}
	for i, s := range t.steps {
		if !t.from(i, ev) {
			continue
		}
		if s.Analyzer {
			if (level == "high" || level == "medium") && strings.EqualFold(op, s.SeenOp) {
				t.done[i] = true
			}
			continue
		}
		if s.SeenKind == "" || string(ev.Kind) != s.SeenKind {
			continue
		}
		if s.SeenTable != "" && !slices.ContainsFunc(ev.Tables, func(tb string) bool { return strings.EqualFold(tb, s.SeenTable) }) {
			continue
		}
		if s.SeenOp == "" || strings.EqualFold(op, s.SeenOp) {
			t.done[i] = true
		}
	}
}

// from reports whether ev crossed the listener in front of step i's demo.
func (t *tour) from(i int, ev audit.Event) bool {
	l := t.target(i).Listener
	return l == "" || ev.Connection == "" || ev.Connection == l
}

// run sends step i's request off the UI goroutine, over the demo's protocol.
func (t *tour) run(i int) tea.Cmd {
	t.running, t.flash = i, ""
	if t.steps[i].Analyzer {
		t.awaiting, t.verdict = i, nil
	}
	if t.pgAt(i) {
		return t.runPG(i)
	}
	return t.runHTTP(i)
}

func (t *tour) runHTTP(i int) tea.Cmd {
	s, ports := t.steps[i], t.target(i).Ports
	return func() tea.Msg {
		timeout := 5 * time.Second
		if s.Analyzer {
			timeout = analyzerTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var body io.Reader
		if s.Body != "" {
			body = strings.NewReader(s.Body)
		}
		req, err := http.NewRequestWithContext(ctx, s.Method, s.URL(ports), body)
		if err != nil {
			return tourResultMsg{step: i, err: err}
		}
		if s.Body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return tourResultMsg{step: i, err: err}
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return tourResultMsg{step: i, status: resp.StatusCode, body: string(b), dur: time.Since(start)}
	}
}

// runPG runs step i's statement as the demo's read-only role. Through the
// sidecar it connects to the loopback listener in plaintext, the way the
// listener serves; the comparison step connects to the database itself over
// TLS, verified against the RDS roots the demo carries.
func (t *tour) runPG(i int) tea.Cmd {
	s, ports := t.steps[i], t.target(i).Ports
	target := pgTarget{addr: ports.Listen}
	return func() tea.Msg {
		if s.Direct {
			roots, err := sidecardemo.RDSRoots()
			if err != nil {
				return tourResultMsg{step: i, err: err}
			}
			host, _, _ := net.SplitHostPort(ports.API)
			target = pgTarget{addr: ports.API, tls: &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12}}
		}
		start := time.Now()
		res, err := pgQuery(target, s.SQL, s.Analyzer)
		if err != nil {
			return tourResultMsg{step: i, err: err}
		}
		res.step, res.dur = i, time.Since(start)
		return tourResultMsg(res)
	}
}

func (t *tour) apply(r tourResult) {
	t.running = -1
	t.result = &r
	if r.err == nil {
		t.done[r.step] = true
	}
}

// summary is the one-line "what runs" for a step: the HTTP method and path, or
// the SQL statement.
func (t *tour) summary(i int) string {
	s := t.steps[i]
	if t.pgAt(i) {
		return s.SQL
	}
	return s.Method + " " + s.Path
}

// statusText names a result's status the way the view reads it. HTTP uses its
// own phrases; Postgres has only an answer or a refusal.
func (t *tour) statusText(status int) string {
	if t.pg() {
		switch {
		case status == 403:
			return "refused"
		case status >= 400:
			return "error"
		}
		return "ok"
	}
	return http.StatusText(status)
}

// key handles the section's own keys and reports whether it used k.
func (t *tour) key(k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "up", "k":
		t.cur = max(t.cur-1, 0)
	case "down", "j":
		t.cur = min(t.cur+1, len(t.steps)-1)
	case "enter", "space", "r":
		if t.running < 0 {
			return t.run(t.cur), true
		}
	case "o":
		if t.pg() {
			t.flash = "the Postgres demo has no browser step; copy the psql command with c"
			break
		}
		s := t.steps[t.cur]
		switch {
		case t.open == nil:
			t.flash = "no browser opener on this system; copy the curl instead"
		case s.Method != "GET":
			t.flash = "a browser can only send GET; run this one with enter or curl"
		default:
			if err := t.open(s.URL(t.target(t.cur).Ports)); err != nil {
				t.flash = "could not open a browser: " + err.Error()
			} else {
				t.flash = "opened " + s.URL(t.target(t.cur).Ports) + " in your browser"
			}
		}
	case "c":
		label := "curl"
		if t.pg() {
			label = "psql"
		}
		t.flash = "copied the " + label + " command"
		return tea.SetClipboard(t.command(t.cur)), true
	default:
		return nil, false
	}
	return nil, true
}

func (t *tour) hints() []keyHint {
	keys := []keyHint{{"↑↓", "step"}, {"enter", "run it here"}}
	copyLabel := "copy curl"
	if t.pg() {
		copyLabel = "copy psql"
	} else if t.open != nil && t.steps[t.cur].Method == "GET" {
		keys = append(keys, keyHint{"o", "open in browser"})
	}
	return append(keys, keyHint{"c", copyLabel}, keyHint{"1", "see it in Wire"}, keyHint{"q", "quit"})
}

func (t *tour) view(w, h int, now time.Time) string {
	done := 0
	for _, d := range t.done {
		if d {
			done++
		}
	}
	text := lipgloss.NewStyle().Width(w)
	intro := t.intro()
	lines := []string{
		stStrong.Render("Try the demo") + stFaint.Render(fmt.Sprintf("   %d of %d done", done, len(t.steps))),
		text.Inherit(stText).Render(intro),
		"",
	}
	titleW := 0
	for _, s := range t.steps {
		titleW = max(titleW, ansi.StringWidth(s.Title))
	}
	for i, s := range t.steps {
		// One heading per demo, only when there is more than one to tell apart.
		if len(t.targets) > 1 && (i == 0 || t.at[i] != t.at[i-1]) {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, stLabel.Render(strings.ToUpper(t.target(i).label())))
		}
		mark := badge(fmt.Sprint(i+1), colPrimary)
		if t.done[i] {
			mark = stPrimary.Render(" ✓ ")
		}
		cursor, title := "  ", stText.Render(fmt.Sprintf("%-*s", titleW, s.Title))
		if i == t.cur {
			cursor, title = stPrimary.Render("› "), stStrong.Render(fmt.Sprintf("%-*s", titleW, s.Title))
		}
		target := t.summary(i)
		if s.Direct {
			target += stFaint.Render("  (" + t.target(i).Ports.API + ", no sidecar)")
		}
		row := cursor + mark + "  " + title + "  " + stFaint.Render(target)
		if i == t.cur {
			row = withBackground(ansi.Truncate(row, w, "…"), w)
		}
		lines = append(lines, ansi.Truncate(row, w, "…"))
	}

	s := t.steps[t.cur]
	lines = append(lines, "", stLabel.Render(strings.ToUpper(s.Title)),
		text.Inherit(stText).Render("Expect: "+s.Expect),
		stFaint.Render("$ ")+stKey.Render(ansi.Truncate(t.command(t.cur), w-2, "…")))
	if t.flash != "" {
		lines = append(lines, stPrimary.Render(t.flash))
	}
	lines = append(lines, "")

	switch r := t.result; {
	case t.running == t.cur:
		verb := "sending "
		if t.pg() {
			verb = "running "
		}
		lines = append(lines, shimmer(verb+t.summary(t.cur)+"…", now))
	case r != nil && r.step == t.cur && r.err != nil:
		lines = append(lines, stDanger.Bold(true).Render("✕ "+r.err.Error()))
	case r != nil && r.step == t.cur:
		st := stPrimary
		if r.status >= 400 {
			st = stDanger.Bold(true)
		}
		lines = append(lines, st.Render(fmt.Sprintf("%d %s", r.status, t.statusText(r.status)))+
			stFaint.Render("  ·  "+short(r.dur)))
		if s.Analyzer {
			lines = append(lines, t.verdictLine())
		}
		lines = append(lines, text.Inherit(stFaint).Render(s.Why))
		lines = append(lines, t.next(), "")
		room := max(h-len(lines)-1, 3)
		body := strings.Split(strings.TrimRight(r.body, "\n"), "\n")
		for i, l := range body {
			if i == room {
				lines = append(lines, stFaint.Render(fmt.Sprintf("  … %d more lines", len(body)-room)))
				break
			}
			lines = append(lines, "  "+highlightMasked(ansi.Truncate(l, w-2, "…")))
		}
	default:
		lines = append(lines, stFaint.Render("Press enter to send it and see the answer here."))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// intro says where the steps go: one demo, or each of them.
func (t *tour) intro() string {
	const tail = "then open Wire to see what the sidecar recorded."
	if len(t.targets) == 1 {
		d := t.targets[0]
		if d.Kind == sidecardemo.KindPostgres {
			return "Statements sent to " + d.Ports.Listen + " go through the sidecar to hoop's demo database, " +
				"a read-only copy of the dellstore sample shop. " +
				"Run each step here, or paste its psql into another terminal, " + tail
		}
		return "Requests to " + d.Ports.Listen + " go through the sidecar to an invented API. " +
			"Run each step here, or paste its curl into another terminal, " + tail
	}
	var parts []string
	for _, d := range t.targets {
		if d.Kind == sidecardemo.KindPostgres {
			parts = append(parts, d.Ports.Listen+" fronts hoop's demo database, a read-only copy of the dellstore sample shop")
		} else {
			parts = append(parts, d.Ports.Listen+" fronts an invented API")
		}
	}
	return "The sidecar runs every demo at once: " + strings.Join(parts, "; ") + ". " +
		"Run each step here, or paste its command into another terminal, " + tail
}

// verdictLine is what the analyzer recorded for the step that shows it: the
// level and what it mapped to, or why there is no level (the model was not
// reached, the budget is spent).
func (t *tour) verdictLine() string {
	v := t.verdict
	if v == nil || t.awaiting != t.cur {
		return stFaint.Render("analyzer: waiting for its record in the audit trail…")
	}
	if v.level == "" {
		return stDanger.Bold(true).Render("analyzer: no rating") + stFaint.Render(" · ai_status "+v.status)
	}
	line := stLabel.Render("analyzer ") + riskBadge(v.level)
	if v.action != "" {
		line += stText.Render(" → " + v.action)
	}
	if v.message != "" {
		line += stFaint.Render("  " + v.message)
	}
	return line
}

// next points at the first step not done yet, or at Wire once all are.
func (t *tour) next() string {
	for i, d := range t.done {
		if !d && i != t.cur {
			arrow := "↓"
			if i < t.cur {
				arrow = "↑"
			}
			return stText.Render("Next: ") + stKey.Render(arrow) + " " + stStrong.Render(t.steps[i].Title)
		}
	}
	return stPrimary.Render("✓ All done. ") + stText.Render("Press ") + stKey.Render("1") +
		stText.Render(" for Wire: every request above, as the sidecar recorded it.")
}

// pgTarget is where a tour statement goes: an address, and the TLS to speak
// there (nil for plaintext).
type pgTarget struct {
	addr string
	tls  *tls.Config
}

// sqlstateRefused is the SQLSTATE the sidecar's Postgres denial carries
// (proxy.PostgresError: 42501 insufficient_privilege, severity FATAL).
const sqlstateRefused = "42501"

// pgQuery runs one statement, over the simple query protocol the way psql
// does, as the demo's read-only role, and returns the result as the tour shows
// it: rows rendered as text, or a command tag. A Postgres error is an answer
// too, not a failure of the step: the sidecar's refusal is a 403 the view
// styles like a refused HTTP request, and any other (a mistyped column, a
// failed login) a 400 that carries the database's own message. Only a
// connection that never answered is the returned error.
func pgQuery(t pgTarget, sql string, analyzed bool) (tourResult, error) {
	host, port, err := net.SplitHostPort(t.addr)
	if err != nil {
		return tourResult{}, err
	}
	// pgx reads every PG* variable before it reads the connection string,
	// and has no way to skip them. So every setting one of them could feed
	// is given here, and a stray PGPORT or PGSSLMODE in the person's shell
	// cannot break the step or send it elsewhere. TLS is set below, after
	// parsing, so sslmode=disable only keeps the file settings unread.
	// PGSERVICE is the one a connection string cannot clear: a broken
	// service file fails the step with pgx's own message.
	cfg, err := pgx.ParseConfig(sidecardemo.ConnString([][2]string{
		{"host", host}, {"port", port},
		{"user", sidecardemo.PGUser}, {"password", sidecardemo.PGPassword}, {"passfile", ""},
		{"dbname", sidecardemo.PGDatabase},
		{"sslmode", "disable"}, {"sslrootcert", ""}, {"sslcert", ""}, {"sslkey", ""},
		{"sslpassword", ""}, {"sslsni", "1"}, {"sslnegotiation", "postgres"},
		{"connect_timeout", "10"}, {"target_session_attrs", "any"},
		{"min_protocol_version", "3.0"}, {"max_protocol_version", "3.0"},
	}...))
	if err != nil {
		return tourResult{}, err
	}
	cfg.TLSConfig, cfg.Fallbacks = t.tls, nil
	cfg.RuntimeParams = map[string]string{"application_name": "hoop sidecar demo"}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	timeout := 15 * time.Second
	if analyzed {
		timeout = analyzerTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return pgAnswer(err)
	}
	// Close only sends Terminate. The answer is read in full before it runs,
	// so a close that fails cannot change it; its error is dropped on purpose.
	defer func() { _ = conn.Close(context.Background()) }()

	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return pgAnswer(err)
	}
	var cols []string
	for _, f := range rows.FieldDescriptions() {
		cols = append(cols, f.Name)
	}
	var out [][]string
	for rows.Next() {
		raw := rows.RawValues()
		row := make([]string, len(raw))
		for i, v := range raw {
			if v == nil {
				row[i] = "NULL"
			} else {
				row[i] = string(v)
			}
		}
		out = append(out, row)
	}
	// rows.Close returns nothing; rows.Err reports what the read and the
	// close ran into, the server's own error frame included.
	rows.Close()
	if err := rows.Err(); err != nil {
		return pgAnswer(err)
	}
	if len(cols) == 0 {
		return tourResult{status: 200, body: rows.CommandTag().String()}, nil
	}
	return tourResult{status: 200, body: renderTable(cols, out)}, nil
}

// pgAnswer turns a Postgres error into the step's answer, and passes any other
// error through as the step's failure.
func pgAnswer(err error) (tourResult, error) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return tourResult{}, err
	}
	status := 400
	if pgErr.Code == sqlstateRefused && pgErr.Severity == "FATAL" {
		status = 403
	}
	return tourResult{status: status, body: pgErr.Message}, nil
}

// renderTable lays rows out as a simple fixed-width table, header first. The
// values are the ones the sidecar let through, so a masked column already
// reads [REDACTED:…] here and highlightMasked colors it.
func renderTable(cols []string, rows [][]string) string {
	width := make([]int, len(cols))
	for i, c := range cols {
		width[i] = ansi.StringWidth(c)
	}
	for _, r := range rows {
		for i, v := range r {
			if i < len(width) && ansi.StringWidth(v) > width[i] {
				width[i] = ansi.StringWidth(v)
			}
		}
	}
	pad := func(cells []string) string {
		parts := make([]string, len(cells))
		for i, v := range cells {
			n := 0
			if i < len(width) {
				n = width[i]
			}
			parts[i] = v + strings.Repeat(" ", max(n-ansi.StringWidth(v), 0))
		}
		return strings.TrimRight(strings.Join(parts, "  "), " ")
	}
	lines := []string{pad(cols)}
	sep := make([]string, len(cols))
	for i := range sep {
		sep[i] = strings.Repeat("-", width[i])
	}
	lines = append(lines, pad(sep))
	for _, r := range rows {
		lines = append(lines, pad(r))
	}
	return strings.Join(lines, "\n")
}

// highlightMasked draws a masked value in the brand blue, so the eye finds
// what the sidecar changed.
func highlightMasked(line string) string {
	i := strings.Index(line, "[REDACTED")
	if i < 0 {
		return stText.Render(line)
	}
	j := strings.Index(line[i:], "]")
	if j < 0 {
		return stText.Render(line)
	}
	return stText.Render(line[:i]) + stPrimary.Render(line[i:i+j+1]) + highlightMasked(line[i+j+1:])
}
