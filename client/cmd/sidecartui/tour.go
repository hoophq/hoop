package sidecartui

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jackc/pgx/v5/pgproto3"

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
	// Ports are where this demo runs; the zero value means the defaults.
	Ports sidecardemo.Ports
	// Kind is which demo this is: HTTP or Postgres. The zero value is HTTP.
	Kind sidecardemo.Kind
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
	kind    sidecardemo.Kind
	steps   []sidecardemo.Step
	cur     int
	done    []bool
	running int
	result  *tourResult
	open    func(string) error
	flash   string
	ports   sidecardemo.Ports
	// lastOp is each session's last recorded operation, which a masked
	// event (it names none) is matched by.
	lastOp map[string]string
}

func newTour(o *DemoOptions) *tour {
	kind := o.Kind
	if kind == "" {
		kind = sidecardemo.KindHTTP
	}
	p := o.Ports
	if p.Listen == "" || p.API == "" {
		p = sidecardemo.DefaultPortsFor(kind)
	}
	steps := sidecardemo.StepsFor(kind)
	return &tour{kind: kind, steps: steps, done: make([]bool, len(steps)), running: -1,
		open: o.OpenURL, ports: p}
}

// pg reports whether this is the Postgres demo.
func (t *tour) pg() bool { return t.kind == sidecardemo.KindPostgres }

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
	for i, s := range t.steps {
		if s.SeenKind == "" || string(ev.Kind) != s.SeenKind {
			continue
		}
		if s.SeenOp == "" || strings.EqualFold(op, s.SeenOp) {
			t.done[i] = true
		}
	}
}

// run sends step i's request off the UI goroutine, over the demo's protocol.
func (t *tour) run(i int) tea.Cmd {
	t.running, t.flash = i, ""
	if t.pg() {
		return t.runPG(i)
	}
	return t.runHTTP(i)
}

func (t *tour) runHTTP(i int) tea.Cmd {
	s := t.steps[i]
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var body io.Reader
		if s.Body != "" {
			body = strings.NewReader(s.Body)
		}
		req, err := http.NewRequestWithContext(ctx, s.Method, s.URL(t.ports), body)
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

func (t *tour) runPG(i int) tea.Cmd {
	s := t.steps[i]
	addr := t.ports.Listen
	if s.Direct {
		addr = t.ports.API
	}
	return func() tea.Msg {
		start := time.Now()
		res, err := pgQuery(addr, s.SQL)
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
func (t *tour) summary(s sidecardemo.Step) string {
	if t.pg() {
		return s.SQL
	}
	return s.Method + " " + s.Path
}

// statusText names a result's status the way the view reads it. HTTP uses its
// own phrases; Postgres has only an answer or a refusal.
func (t *tour) statusText(status int) string {
	if t.pg() {
		if status >= 400 {
			return "refused"
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
			if err := t.open(s.URL(t.ports)); err != nil {
				t.flash = "could not open a browser: " + err.Error()
			} else {
				t.flash = "opened " + s.URL(t.ports) + " in your browser"
			}
		}
	case "c":
		label := "curl"
		if t.pg() {
			label = "psql"
		}
		t.flash = "copied the " + label + " command"
		return tea.SetClipboard(t.steps[t.cur].Command(t.kind, t.ports)), true
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
	intro := "Requests to " + t.ports.Listen + " go through the sidecar to an invented API. " +
		"Run each step here, or paste its curl into another terminal, then open Wire to see what the sidecar recorded."
	if t.pg() {
		intro = "Statements sent to " + t.ports.Listen + " go through the sidecar to an invented database. " +
			"Run each step here, or paste its psql into another terminal, then open Wire to see what the sidecar recorded."
	}
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
		mark := badge(fmt.Sprint(i+1), colPrimary)
		if t.done[i] {
			mark = stPrimary.Render(" ✓ ")
		}
		cursor, title := "  ", stText.Render(fmt.Sprintf("%-*s", titleW, s.Title))
		if i == t.cur {
			cursor, title = stPrimary.Render("› "), stStrong.Render(fmt.Sprintf("%-*s", titleW, s.Title))
		}
		target := t.summary(s)
		if s.Direct {
			target += stFaint.Render("  (" + t.ports.API + ", no sidecar)")
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
		stFaint.Render("$ ")+stKey.Render(ansi.Truncate(s.Command(t.kind, t.ports), w-2, "…")))
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
		lines = append(lines, shimmer(verb+t.summary(s)+"…", now))
	case r != nil && r.step == t.cur && r.err != nil:
		lines = append(lines, stDanger.Bold(true).Render("✕ "+r.err.Error()))
	case r != nil && r.step == t.cur:
		st := stPrimary
		if r.status >= 400 {
			st = stDanger.Bold(true)
		}
		lines = append(lines, st.Render(fmt.Sprintf("%d %s", r.status, t.statusText(r.status)))+
			stFaint.Render("  ·  "+short(r.dur)))
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

// pgQuery runs one simple-protocol statement against addr and returns the
// result as the tour shows it: rows rendered as text, a command tag, or a
// refusal. A connection failure is the returned error; a Postgres error frame
// (a guardrail denial among them) is a result with a 403 status, so the view
// styles it like a refused HTTP request.
func pgQuery(addr, sql string) (tourResult, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return tourResult{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	fe := pgproto3.NewFrontend(conn, conn)
	fe.Send(&pgproto3.StartupMessage{
		ProtocolVersion: pgproto3.ProtocolVersionNumber,
		Parameters:      map[string]string{"user": "demo", "database": "postgres"},
	})
	if err := fe.Flush(); err != nil {
		return tourResult{}, err
	}
	if err := pgDrain(fe); err != nil {
		return tourResult{}, err
	}

	fe.Send(&pgproto3.Query{String: sql})
	if err := fe.Flush(); err != nil {
		return tourResult{}, err
	}
	return pgResult(fe)
}

// pgDrain reads the startup exchange up to the first ReadyForQuery.
func pgDrain(fe *pgproto3.Frontend) error {
	for {
		msg, err := fe.Receive()
		if err != nil {
			return err
		}
		switch m := msg.(type) {
		case *pgproto3.ErrorResponse:
			return fmt.Errorf("%s", m.Message)
		case *pgproto3.ReadyForQuery:
			return nil
		}
	}
}

// pgResult reads one statement's response up to ReadyForQuery and renders it.
func pgResult(fe *pgproto3.Frontend) (tourResult, error) {
	var cols []string
	var rows [][]string
	var tag, errMsg string
	status := 200
	for {
		msg, err := fe.Receive()
		if err != nil {
			// A guardrail denial is a FATAL ErrorResponse followed by the
			// sidecar closing the connection (proxy.PostgresError): no
			// ReadyForQuery comes after it. The close is how a refusal ends,
			// not a failure of the step.
			if errMsg != "" {
				return tourResult{status: status, body: errMsg}, nil
			}
			return tourResult{}, err
		}
		switch m := msg.(type) {
		case *pgproto3.RowDescription:
			cols = cols[:0]
			for _, f := range m.Fields {
				cols = append(cols, string(f.Name))
			}
		case *pgproto3.DataRow:
			row := make([]string, len(m.Values))
			for i, v := range m.Values {
				if v == nil {
					row[i] = "NULL"
				} else {
					row[i] = string(v)
				}
			}
			rows = append(rows, row)
		case *pgproto3.CommandComplete:
			tag = string(m.CommandTag)
		case *pgproto3.EmptyQueryResponse:
			tag = "(empty query)"
		case *pgproto3.ErrorResponse:
			// A guardrail denial reaches the client as a Postgres error frame.
			status, errMsg = 403, m.Message
		case *pgproto3.ReadyForQuery:
			switch {
			case errMsg != "":
				return tourResult{status: status, body: errMsg}, nil
			case len(cols) > 0:
				return tourResult{status: 200, body: renderTable(cols, rows)}, nil
			default:
				return tourResult{status: 200, body: tag}, nil
			}
		}
	}
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
