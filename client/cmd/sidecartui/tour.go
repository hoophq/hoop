package sidecartui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	"github.com/hoophq/hoop/sidecar/audit"
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
	steps   []sidecardemo.Step
	cur     int
	done    []bool
	running int
	result  *tourResult
	open    func(string) error
	flash   string
	ports   sidecardemo.Ports
}

func newTour(o *DemoOptions) *tour {
	p := o.Ports
	if p.Listen == "" || p.API == "" {
		p = sidecardemo.DefaultPorts
	}
	return &tour{steps: sidecardemo.Steps, done: make([]bool, len(sidecardemo.Steps)), running: -1,
		open: o.OpenURL, ports: p}
}

// see ticks the step an audit event shows happened.
func (t *tour) see(ev audit.Event) {
	for i, s := range t.steps {
		if s.SeenKind == "" || string(ev.Kind) != s.SeenKind {
			continue
		}
		if s.SeenOp == "" || strings.EqualFold(string(ev.Operation), s.SeenOp) {
			t.done[i] = true
		}
	}
}

// run sends step i's request off the UI goroutine.
func (t *tour) run(i int) tea.Cmd {
	t.running, t.flash = i, ""
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

func (t *tour) apply(r tourResult) {
	t.running = -1
	t.result = &r
	if r.err == nil {
		t.done[r.step] = true
	}
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
		t.flash = "copied the curl command"
		return tea.SetClipboard(t.steps[t.cur].Curl(t.ports)), true
	default:
		return nil, false
	}
	return nil, true
}

func (t *tour) hints() []keyHint {
	keys := []keyHint{{"↑↓", "step"}, {"enter", "run it here"}}
	if t.open != nil && t.steps[t.cur].Method == "GET" {
		keys = append(keys, keyHint{"o", "open in browser"})
	}
	return append(keys, keyHint{"c", "copy curl"}, keyHint{"1", "see it in Wire"}, keyHint{"q", "quit"})
}

func (t *tour) view(w, h int, now time.Time) string {
	done := 0
	for _, d := range t.done {
		if d {
			done++
		}
	}
	text := lipgloss.NewStyle().Width(w)
	lines := []string{
		stStrong.Render("Try the demo") + stFaint.Render(fmt.Sprintf("   %d of %d done", done, len(t.steps))),
		text.Inherit(stText).Render("Requests to " + t.ports.Listen + " go through the sidecar to an invented API. " +
			"Run each step here, or paste its curl into another terminal, then open Wire to see what the sidecar recorded."),
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
		target := s.Method + " " + s.Path
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
		stFaint.Render("$ ")+stKey.Render(ansi.Truncate(s.Curl(t.ports), w-2, "…")))
	if t.flash != "" {
		lines = append(lines, stPrimary.Render(t.flash))
	}
	lines = append(lines, "")

	switch r := t.result; {
	case t.running == t.cur:
		lines = append(lines, shimmer("sending "+s.Method+" "+s.Path+"…", now))
	case r != nil && r.step == t.cur && r.err != nil:
		lines = append(lines, stDanger.Bold(true).Render("✕ "+r.err.Error()))
	case r != nil && r.step == t.cur:
		st := stPrimary
		if r.status >= 400 {
			st = stDanger.Bold(true)
		}
		lines = append(lines, st.Render(fmt.Sprintf("%d %s", r.status, http.StatusText(r.status)))+
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
