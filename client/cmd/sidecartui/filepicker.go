package sidecartui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The file picker is a path you type with the folder it names listed under
// it, so nobody has to know a path by heart: typing narrows the list, the
// arrows move through it, tab or → steps into a folder or completes a file,
// and enter on a file chooses it. Only folders and config files are listed.

// isConfigFile is whether name has an extension the sidecar loads.
func isConfigFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json":
		return true
	}
	return false
}

// configsIn lists the YAML configs in dir, by name, skipping hidden files
// (the setup screen's own temporary checks among them). JSON is left out
// on purpose: a folder is full of JSON that is not a sidecar config, and a
// suggestion list of package.json files would be noise.
func configsIn(dir string) []string {
	if dir == "" {
		dir = "."
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		ext := strings.ToLower(filepath.Ext(n))
		if e.IsDir() || strings.HasPrefix(n, ".") || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		out = append(out, filepath.Join(dir, n))
	}
	slices.Sort(out)
	return out
}

type pickEntry struct {
	name string
	dir  bool
}

type filePicker struct {
	input   textinput.Model
	entries []pickEntry
	cur     int
	// note says why the listing is empty: an unreadable folder, or one
	// with no folders or configs in it.
	note string
}

func newFilePicker(start string) *filePicker {
	ti := textinput.New()
	ti.Prompt = "Path  "
	ti.Placeholder = "./"
	ti.CharLimit = 4096
	if start == "" {
		start = "."
	}
	ti.SetValue(withSlash(start))
	ti.CursorEnd()
	ti.Focus()
	p := &filePicker{input: ti}
	p.refresh()
	if len(p.entries) > 1 && p.entries[0].name == ".." {
		p.cur = 1
	}
	return p
}

func withSlash(dir string) string {
	if strings.HasSuffix(dir, string(os.PathSeparator)) {
		return dir
	}
	return dir + string(os.PathSeparator)
}

// expand turns a leading ~ into the home directory, which a shell would do
// and an os.ReadDir does not.
func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~"+string(os.PathSeparator)) {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// split cuts the typed value into the folder to list and the prefix that
// narrows it: "conf/hoop" lists conf/ for names starting with "hoop".
func (p *filePicker) split() (dir, prefix string) {
	v := expand(p.input.Value())
	if v == "" {
		return ".", ""
	}
	if strings.HasSuffix(v, string(os.PathSeparator)) {
		return v, ""
	}
	return filepath.Dir(v), filepath.Base(v)
}

func (p *filePicker) refresh() {
	dir, prefix := p.split()
	p.entries, p.note = nil, ""
	ents, err := os.ReadDir(dir)
	if err != nil {
		p.note = "Cannot read " + dir + ": no such folder, or no permission."
		return
	}
	lp := strings.ToLower(prefix)
	var dirs, files []pickEntry
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(n), lp) {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(dir, n)); err == nil {
				isDir = fi.IsDir()
			}
		}
		switch {
		case isDir:
			dirs = append(dirs, pickEntry{n, true})
		case isConfigFile(n):
			files = append(files, pickEntry{n, false})
		}
	}
	if prefix == "" && filepath.Dir(filepath.Clean(dir)) != filepath.Clean(dir) {
		p.entries = append(p.entries, pickEntry{"..", true})
	}
	p.entries = append(append(p.entries, dirs...), files...)
	p.cur = min(p.cur, max(len(p.entries)-1, 0))
	if len(p.entries) == 0 {
		p.note = "No folders or config files here match."
	}
}

// path is the full path of entry e in the listed folder.
func (p *filePicker) path(e pickEntry) string {
	dir, _ := p.split()
	return filepath.Join(dir, e.name)
}

// step moves into entry e: a folder becomes the listed one, a file is
// completed in the input.
func (p *filePicker) step(e pickEntry) {
	v := p.path(e)
	if e.dir {
		v = withSlash(filepath.Clean(v))
	}
	p.input.SetValue(v)
	p.input.CursorEnd()
	p.cur = 0
	p.refresh()
	// Into a folder, the cursor starts on what is in it, not on "..":
	// enter twice must go deeper, never back where it came from.
	if len(p.entries) > 1 && p.entries[0].name == ".." {
		p.cur = 1
	}
}

// shortPath writes path the short way a person reads it: relative to the
// current folder when it is under it, from ~ when it is under home.
func shortPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(abs, home+string(os.PathSeparator)) {
		return "~" + abs[len(home):]
	}
	return abs
}

// update returns the chosen file's path, "back" for esc, or "".
func (p *filePicker) update(k tea.KeyPressMsg) string {
	switch k.String() {
	case "esc":
		return "back"
	case "up", "shift+tab":
		p.cur = max(p.cur-1, 0)
		return ""
	case "down":
		p.cur = min(p.cur+1, max(len(p.entries)-1, 0))
		return ""
	case "tab", "right":
		if p.cur < len(p.entries) {
			p.step(p.entries[p.cur])
		}
		return ""
	case "enter":
		if p.cur < len(p.entries) {
			e := p.entries[p.cur]
			if e.dir {
				p.step(e)
				return ""
			}
			return p.path(e)
		}
		// Nothing listed: the typed path is taken as it is, so a file
		// with another extension can still be named in full.
		if v := strings.TrimSpace(expand(p.input.Value())); v != "" && !strings.HasSuffix(v, string(os.PathSeparator)) {
			return v
		}
		return ""
	}
	p.input, _ = p.input.Update(k)
	p.cur = 0
	p.refresh()
	return ""
}

func (p *filePicker) view(w, h int) string {
	head := []string{p.input.View(), ""}
	var rows []string
	for i, e := range p.entries {
		name := e.name
		st := stText
		icon := stFaint.Render("  ")
		if e.dir {
			name += string(os.PathSeparator)
			icon = stKey.Render("▸ ")
		} else {
			icon = stPrimary.Render("◆ ")
		}
		if i == p.cur {
			st = stStrong
		}
		row := "  " + icon + st.Render(name)
		if !e.dir && i == p.cur {
			row += stFaint.Render("   enter to open it")
		}
		row = ansi.Truncate(row, w, "…")
		if i == p.cur {
			row = withBackground(row, w)
		}
		rows = append(rows, row)
	}
	if p.note != "" {
		rows = append(rows, stFaint.Render("  "+p.note))
	}
	return strings.Join(head, "\n") + "\n" + window(rows, p.cur, max(h-len(head), 3))
}
