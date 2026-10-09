package sidecartui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The setup screens are built from three widgets: a menu (pick one row), a
// form (edit a record field by field) and a checklist (pick several of a
// long list, with a filter). Each returns an event string to its page
// instead of acting, so the pages own every decision.

// ---- menu -----------------------------------------------------------------

type menuItem struct {
	id     string
	label  string
	detail string
	// button rows are drawn as actions, not records.
	button bool
	dim    bool
	// note rows are a line of text between groups: drawn faint, with a
	// blank line around them, and skipped by the cursor.
	note bool
}

type menu struct {
	items []menuItem
	cur   int
}

// step moves the cursor by delta, past note rows.
func (m *menu) step(delta int) {
	n := len(m.items)
	for range n {
		m.cur = ((m.cur+delta)%n + n) % n
		if !m.items[m.cur].note {
			return
		}
	}
}

// update moves the cursor and returns the id of the row enter chose, or
// "back" for esc. Any other key comes back as "key:<name>" for the page.
func (m *menu) update(k tea.KeyPressMsg) string {
	if len(m.items) == 0 {
		return ""
	}
	switch k.String() {
	case "up", "k", "shift+tab":
		m.step(-1)
	case "down", "j", "tab":
		m.step(1)
	case "enter", "right", "l":
		if m.cur < len(m.items) {
			return m.items[m.cur].id
		}
	case "esc", "left", "h":
		return "back"
	default:
		return "key:" + k.String()
	}
	return ""
}

func (m menu) selected() string {
	if m.cur < len(m.items) {
		return m.items[m.cur].id
	}
	return ""
}

func (m menu) view(w, h int) string {
	labelW := 0
	for _, it := range m.items {
		if !it.button && !it.note {
			labelW = max(labelW, ansi.StringWidth(it.label))
		}
	}
	labelW = min(labelW, w/2)
	var rows []string
	curRow := 0
	for i, it := range m.items {
		focused := i == m.cur
		if it.note {
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			for _, l := range strings.Split(lipgloss.NewStyle().Width(max(w-2, 10)).Render(it.label), "\n") {
				rows = append(rows, "  "+stFaint.Render(l))
			}
			rows = append(rows, "")
			continue
		}
		var row string
		switch {
		case it.button && focused:
			row = "  " + badge(buttonGlyph(it.id)+it.label, colPrimary)
		case it.button && it.dim:
			row = "  " + stFaint.Render("  "+it.label)
		case it.button:
			row = "  " + stPrimary.Render("  "+it.label)
		default:
			mark := "  "
			lst := stText
			if focused {
				mark = stPrimary.Render("› ")
				lst = stStrong
			}
			row = mark + lst.Render(fmt.Sprintf("%-*s", labelW, ansi.Truncate(it.label, labelW, "…"))) +
				"  " + stFaint.Render(it.detail)
		}
		row = ansi.Truncate(row, w, "…")
		if focused && !it.button {
			row = withBackground(row, w)
		}
		// A button group that follows records is set off by a blank line.
		if it.button && i > 0 && !m.items[i-1].button && !m.items[i-1].note {
			rows = append(rows, "")
		}
		if focused {
			curRow = len(rows)
		}
		rows = append(rows, row)
	}
	return window(rows, curRow, h)
}

// buttonGlyph leads a focused button: an arrow pointing where it goes, so
// Back points back.
func buttonGlyph(id string) string {
	if id == "back" {
		return "◀ "
	}
	return "▶ "
}

// window shows h rows of rows, scrolled so row cur is in view.
func window(rows []string, cur, h int) string {
	if h <= 0 || len(rows) <= h {
		return strings.Join(rows, "\n")
	}
	start := min(max(cur-h/2, 0), len(rows)-h)
	return strings.Join(rows[start:start+h], "\n")
}

// ---- form -----------------------------------------------------------------

type fieldKind int

const (
	fText   fieldKind = iota // free text
	fInt                     // a whole number, as text
	fBool                    // on or off
	fEnum                    // one of options
	fMulti                   // several of options, picked in a checklist
	fButton                  // an action: enter returns its id
	fNote                    // a line of text, not editable
)

type field struct {
	id    string
	label string
	help  string
	kind  fieldKind

	text        string
	on          bool
	multi       []string
	options     []string
	optLabel    map[string]string
	placeholder string
	// secret masks a text field's value, on screen and off it: a token.
	secret bool

	// hidden is evaluated on every draw and move, so a field can depend
	// on another one's value (a rule type, a presence toggle).
	hidden func() bool
	// note returns a fNote's text, recomputed on every draw.
	note func() string
}

func (f *field) visible() bool { return f.hidden == nil || !f.hidden() }

// list reads a fText field holding a comma-separated list.
func (f *field) list() []string {
	var out []string
	for _, s := range strings.Split(f.text, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

type form struct {
	title  string
	fields []*field
	cur    int
	input  textinput.Model
	pick   *checklist
	// err is shown above the hints until the next edit.
	err string
}

func newForm(title string, fields ...*field) *form {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 4096
	f := &form{title: title, fields: fields, input: ti}
	f.cur = -1
	f.move(1)
	return f
}

func (f *form) byID(id string) *field {
	for _, x := range f.fields {
		if x.id == id {
			return x
		}
	}
	return nil
}

func (f *form) focused() *field {
	if f.cur >= 0 && f.cur < len(f.fields) {
		return f.fields[f.cur]
	}
	return nil
}

// move steps the cursor by delta over visible, focusable fields, and loads
// a text field into the input.
func (f *form) move(delta int) {
	f.store()
	n := len(f.fields)
	for i := 1; i <= n; i++ {
		c := ((f.cur+delta*i)%n + n) % n
		if f.cur < 0 && delta > 0 {
			c = (i - 1) % n
		}
		if x := f.fields[c]; x.visible() && x.kind != fNote {
			f.cur = c
			break
		}
	}
	f.load()
}

func (f *form) load() {
	if x := f.focused(); x != nil && (x.kind == fText || x.kind == fInt) {
		f.input.SetValue(x.text)
		f.input.Placeholder = x.placeholder
		f.input.EchoMode = textinput.EchoNormal
		if x.secret {
			f.input.EchoMode = textinput.EchoPassword
		}
		f.input.CursorEnd()
		f.input.Focus()
		return
	}
	f.input.Blur()
}

func (f *form) store() {
	if x := f.focused(); x != nil && (x.kind == fText || x.kind == fInt) {
		x.text = f.input.Value()
	}
}

// update handles a key and returns "back" for esc, a button's id for enter
// on it, or "" otherwise.
func (f *form) update(k tea.KeyPressMsg) (tea.Cmd, string) {
	if f.pick != nil {
		done, picked := f.pick.update(k)
		if done {
			if picked != nil {
				f.focused().multi = picked
			}
			f.pick = nil
		}
		return nil, ""
	}
	x := f.focused()
	switch k.String() {
	case "esc":
		f.store()
		return nil, "back"
	case "up", "shift+tab":
		f.move(-1)
		return nil, ""
	case "down", "tab":
		f.move(1)
		return nil, ""
	}
	if x == nil {
		return nil, ""
	}
	f.err = ""
	switch x.kind {
	case fText, fInt:
		if k.String() == "enter" {
			f.move(1)
			return nil, ""
		}
		if x.kind == fInt && len(k.Text) == 1 && (k.Text[0] < '0' || k.Text[0] > '9') {
			return nil, ""
		}
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(k)
		x.text = f.input.Value()
		return cmd, ""
	case fBool:
		switch k.String() {
		case "space", "enter", "left", "right", "x":
			x.on = !x.on
		}
	case fEnum:
		i := max(slices.Index(x.options, x.text), 0)
		switch k.String() {
		case "right", "l", "space", "enter":
			x.text = x.options[(i+1)%len(x.options)]
		case "left", "h":
			x.text = x.options[(i+len(x.options)-1)%len(x.options)]
		}
	case fMulti:
		if k.String() == "enter" || k.String() == "space" || k.String() == "right" {
			f.pick = newChecklist(x.label, x.options, x.optLabel, x.multi)
		}
	case fButton:
		if k.String() == "enter" || k.String() == "space" {
			f.store()
			return nil, x.id
		}
	}
	return nil, ""
}

func (f *form) valueView(x *field, focused bool) string {
	switch x.kind {
	case fText, fInt:
		if focused {
			return f.input.View()
		}
		if x.text == "" {
			return stFaint.Render(orDash(x.placeholder))
		}
		if x.secret {
			return stText.Render(strings.Repeat("•", min(len([]rune(x.text)), 24)))
		}
		return stText.Render(x.text)
	case fBool:
		if x.on {
			return stPrimary.Render("[x] on")
		}
		return stFaint.Render("[ ] off")
	case fEnum:
		label := x.text
		if l, ok := x.optLabel[x.text]; ok {
			label = l
		}
		if focused {
			return stKey.Render("‹ ") + stStrong.Render(label) + stKey.Render(" ›")
		}
		return stText.Render(label)
	case fMulti:
		if len(x.multi) == 0 {
			return stFaint.Render(orDash(x.placeholder))
		}
		s := strings.Join(x.multi, ", ")
		if focused {
			return stStrong.Render(s) + stKey.Render("  ⏎ change")
		}
		return stText.Render(s)
	}
	return ""
}

func (f *form) view(w, h int) string {
	if f.pick != nil {
		return f.pick.view(w, h)
	}
	labelW := 0
	for _, x := range f.fields {
		if x.visible() && x.kind != fButton && x.kind != fNote {
			labelW = max(labelW, ansi.StringWidth(x.label))
		}
	}
	labelW = min(labelW, max(w/3, 12))
	// The input gets the room right of the label. Without a width it shows
	// only the first character of its placeholder, so an empty field read
	// "h" where it meant https://hoop.example.com.
	f.input.SetWidth(max(w-labelW-6, 10))
	var rows []string
	curRow := 0
	var help string
	for i, x := range f.fields {
		if !x.visible() {
			continue
		}
		focused := i == f.cur
		if focused {
			curRow = len(rows)
			help = x.help
		}
		switch x.kind {
		case fNote:
			for _, l := range strings.Split(x.note(), "\n") {
				rows = append(rows, "  "+l)
			}
			continue
		case fButton:
			// A button group after fields is set off by a blank line;
			// one at the top of the form needs none.
			if i > 0 && f.fields[i-1].kind != fButton && f.fields[i-1].kind != fNote {
				rows = append(rows, "")
				if focused {
					curRow++
				}
			}
			if focused {
				rows = append(rows, "  "+badge(buttonGlyph(x.id)+x.label, colPrimary))
			} else {
				rows = append(rows, "  "+stPrimary.Render("  "+x.label))
			}
			continue
		}
		mark, lst := "  ", stLabel
		if focused {
			mark, lst = stPrimary.Render("› "), stStrong
		}
		row := mark + lst.Render(fmt.Sprintf("%-*s", labelW, ansi.Truncate(x.label, labelW, "…"))) + "  " + f.valueView(x, focused)
		rows = append(rows, ansi.Truncate(row, w, "…"))
	}
	foot := []string{""}
	if f.err != "" {
		foot = append(foot, stDanger.Bold(true).Render("✕ "+f.err))
	} else if help != "" {
		foot = append(foot, stFaint.Render(ansi.Truncate(help, w, "…")))
	} else {
		foot = append(foot, "")
	}
	return window(rows, curRow, max(h-len(foot), 3)) + "\n" + strings.Join(foot, "\n")
}

// ---- checklist ------------------------------------------------------------

type checklist struct {
	title   string
	items   []string
	label   map[string]string
	sel     map[string]bool
	cur     int
	filter  textinput.Model
	visible []string
}

func newChecklist(title string, items []string, label map[string]string, selected []string) *checklist {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "type to filter"
	ti.Focus()
	c := &checklist{title: title, items: items, label: label, sel: map[string]bool{}, filter: ti}
	for _, s := range selected {
		c.sel[s] = true
	}
	c.refilter()
	return c
}

func (c *checklist) refilter() {
	q := strings.ToLower(strings.TrimSpace(c.filter.Value()))
	c.visible = c.visible[:0]
	for _, it := range c.items {
		if q == "" || strings.Contains(strings.ToLower(it+" "+c.label[it]), q) {
			c.visible = append(c.visible, it)
		}
	}
	c.cur = min(c.cur, max(len(c.visible)-1, 0))
}

// update returns done when the list closes, with the picked items (in list
// order) for enter, or nil for esc, which keeps what was there.
func (c *checklist) update(k tea.KeyPressMsg) (bool, []string) {
	switch k.String() {
	case "esc":
		return true, nil
	case "enter":
		out := []string{}
		for _, it := range c.items {
			if c.sel[it] {
				out = append(out, it)
			}
		}
		return true, out
	case "up", "shift+tab":
		c.cur = max(c.cur-1, 0)
	case "down", "tab":
		c.cur = min(c.cur+1, max(len(c.visible)-1, 0))
	case "space":
		if c.cur < len(c.visible) {
			it := c.visible[c.cur]
			c.sel[it] = !c.sel[it]
		}
	case "ctrl+a":
		all := true
		for _, it := range c.visible {
			all = all && c.sel[it]
		}
		for _, it := range c.visible {
			c.sel[it] = !all
		}
	default:
		c.filter, _ = c.filter.Update(k)
		c.refilter()
	}
	return false, nil
}

func (c *checklist) view(w, h int) string {
	n := 0
	for _, v := range c.sel {
		if v {
			n++
		}
	}
	head := []string{
		stStrong.Render(c.title) + stFaint.Render(fmt.Sprintf("  %d selected", n)),
		c.filter.View(),
		"",
	}
	var rows []string
	for i, it := range c.visible {
		box := stFaint.Render("[ ]")
		if c.sel[it] {
			box = stPrimary.Render("[x]")
		}
		name := stText.Render(it)
		if i == c.cur {
			name = stStrong.Render(it)
		}
		row := "  " + box + " " + name
		if l := c.label[it]; l != "" {
			row += "  " + stFaint.Render(l)
		}
		row = ansi.Truncate(row, w, "…")
		if i == c.cur {
			row = withBackground(row, w)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		rows = []string{stFaint.Render("  nothing matches")}
	}
	foot := stKey.Render("space") + stFaint.Render(" pick  ") + stKey.Render("ctrl+a") + stFaint.Render(" all shown  ") +
		stKey.Render("enter") + stFaint.Render(" done  ") + stKey.Render("esc") + stFaint.Render(" cancel")
	return strings.Join(head, "\n") + "\n" + window(rows, c.cur, max(h-len(head)-2, 3)) + "\n\n" + foot
}
