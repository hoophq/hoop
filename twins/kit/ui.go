package kit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// GET /_twin/ui renders the document of /_twin/state as a page, in its
// document order: a scalar is a setting, a list or a map is a section, and
// a list of objects is a table. The page fetches itself every 2s and swaps
// <main>, so a person or a headless browser watches a flow happen.
//
// The order comes from the JSON the twin encodes: a struct keeps its field
// order, a map is sorted by key.

// field is one member of a decoded JSON object, in document order.
type field struct {
	Key string
	Val any
}

// object is a decoded JSON object. Every decoded value is an object, a
// []any, a string, a json.Number, a bool or nil.
type object []field

// decodeOrdered decodes one JSON value and keeps the order of the object
// members, which a map[string]any loses.
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch d {
	case '{':
		o := object{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			o = append(o, field{Key: k.(string), Val: v})
		}
		_, err = dec.Token()
		return o, err
	case '[':
		a := []any{}
		for dec.More() {
			v, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err = dec.Token()
		return a, err
	}
	return nil, fmt.Errorf("unexpected %v", d)
}

type uiSetting struct {
	Key string
	Val template.HTML
}

type uiSection struct {
	Key, Title string
	Count      int
	// One of these is set; with none, the section shows "none".
	Table *uiTable
	Chips []string
	Pairs []uiSetting
}

type uiTable struct {
	Columns []string
	Rows    [][]template.HTML
}

type uiPage struct {
	Twin      string
	Settings  []uiSetting
	Sections  []uiSection
	UpdatedAt string
}

func (h *handler) ui(w http.ResponseWriter, _ *http.Request) {
	page, err := h.uiPage()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var b bytes.Buffer
	if err := uiTemplate.Execute(&b, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b.Bytes())
}

func (h *handler) uiPage() (uiPage, error) {
	raw, err := json.Marshal(h.twin.State())
	if err != nil {
		return uiPage{}, fmt.Errorf("encode state: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	state, err := decodeOrdered(dec)
	if err != nil {
		return uiPage{}, fmt.Errorf("decode state: %w", err)
	}
	page := uiPage{Twin: h.twin.Name(), UpdatedAt: time.Now().UTC().Format("15:04:05")}
	o, ok := state.(object)
	if !ok {
		o = object{{Key: "state", Val: state}}
	}
	for _, f := range o {
		if s, ok := section(f.Key, f.Val); ok {
			page.Sections = append(page.Sections, s)
		} else {
			page.Settings = append(page.Settings, uiSetting{Key: f.Key, Val: cell(f.Val)})
		}
	}
	h.mu.Lock()
	faults := make([]any, 0, len(h.faults))
	for _, f := range h.faults {
		faults = append(faults, object{{"path", f.Path}, {"status", json.Number(fmt.Sprint(f.Status))},
			{"body", f.Body}, {"delay_ms", json.Number(fmt.Sprint(f.DelayMS))},
			{"times", json.Number(fmt.Sprint(f.Times))}, {"hits", json.Number(fmt.Sprint(f.Hits))}})
	}
	h.mu.Unlock()
	s, _ := section("faults", faults)
	page.Sections = append(page.Sections, s)
	return page, nil
}

// section renders a list or a map. It reports false for a scalar.
func section(key string, v any) (uiSection, bool) {
	s := uiSection{Key: key, Title: title(key)}
	switch v := v.(type) {
	case []any:
		s.Count = len(v)
		if len(v) == 0 {
			return s, true
		}
		for _, e := range v {
			if _, ok := e.(object); !ok {
				for _, e := range v {
					s.Chips = append(s.Chips, text(e))
				}
				return s, true
			}
		}
		s.Table = table(v)
	case object:
		s.Count = len(v)
		for _, f := range v {
			s.Pairs = append(s.Pairs, uiSetting{Key: f.Key, Val: cell(f.Val)})
		}
	default:
		return s, false
	}
	return s, true
}

// table makes one column per key, in the order the rows first name them.
// Every row is an object.
func table(rows []any) *uiTable {
	t := &uiTable{}
	index := map[string]int{}
	for _, r := range rows {
		for _, f := range r.(object) {
			if _, ok := index[f.Key]; !ok {
				index[f.Key] = len(t.Columns)
				t.Columns = append(t.Columns, f.Key)
			}
		}
	}
	for _, r := range rows {
		row := make([]template.HTML, len(t.Columns))
		for _, f := range r.(object) {
			row[index[f.Key]] = cell(f.Val)
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

// cell renders a value inside a table or a setting: a list of scalars as
// chips, a time as its clock time, an object as compact JSON.
func cell(v any) template.HTML {
	switch v := v.(type) {
	case []any:
		if len(v) == 0 {
			return `<span class="none">none</span>`
		}
		var b strings.Builder
		for _, e := range v {
			if _, ok := e.(object); ok {
				return code(v)
			}
			b.WriteString(`<span class="chip">` + template.HTMLEscapeString(text(e)) + `</span>`)
		}
		return template.HTML(b.String())
	case object:
		return code(v)
	case string:
		if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return template.HTML(fmt.Sprintf(`<time datetime="%s" title="%s">%s</time>`,
				template.HTMLEscapeString(v), template.HTMLEscapeString(v), ts.UTC().Format("15:04:05")))
		}
	case nil:
		return `<span class="none">-</span>`
	}
	return template.HTML(template.HTMLEscapeString(text(v)))
}

func code(v any) template.HTML {
	return template.HTML(`<code>` + template.HTMLEscapeString(compact(v)) + `</code>`)
}

// compact writes a decoded value back as one line of JSON.
func compact(v any) string {
	b, _ := json.Marshal(plain(v))
	return string(b)
}

// plain turns a decoded value back into one encoding/json writes.
func plain(v any) any {
	switch v := v.(type) {
	case object:
		m := make(map[string]any, len(v))
		for _, f := range v {
			m[f.Key] = plain(f.Val)
		}
		return m
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = plain(e)
		}
		return out
	}
	return v
}

func text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case nil:
		return "-"
	case object, []any:
		return compact(v)
	}
	return fmt.Sprint(v)
}

// title turns a state key into a heading: expired_tokens -> Expired tokens.
func title(key string) string {
	s := strings.ReplaceAll(key, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

var uiTemplate = template.Must(template.New("ui").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Twin}} twin</title>
<style>
:root { color-scheme: light dark; --bg: #f7f8fa; --card: #fff; --text: #1c2024; --dim: #60646c; --line: #e4e6eb; --chip: #eef0f4; --accent: #3e63dd; --warn: #b3261e; }
@media (prefers-color-scheme: dark) { :root { --bg: #111317; --card: #1a1d23; --text: #e8eaee; --dim: #9aa0aa; --line: #2a2e36; --chip: #262a32; --accent: #8da4ef; --warn: #f2b8b5; } }
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--text); font: 14px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif; }
header { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; padding: 16px 24px; border-bottom: 1px solid var(--line); background: var(--card); }
header h1 { margin: 0; font-size: 18px; }
header .tag { color: var(--dim); font-size: 12px; text-transform: uppercase; letter-spacing: .06em; }
header .live { margin-left: auto; color: var(--dim); font-size: 12px; }
header .live.stale { color: var(--warn); }
header a { color: var(--accent); font-size: 12px; }
main { padding: 16px 24px 32px; display: grid; gap: 16px; max-width: 1200px; }
section { background: var(--card); border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
section h2 { margin: 0; padding: 10px 14px; font-size: 14px; border-bottom: 1px solid var(--line); display: flex; gap: 8px; align-items: center; }
section h2 .count { color: var(--dim); font-weight: normal; }
.scroll { overflow-x: auto; }
table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; padding: 7px 14px; border-bottom: 1px solid var(--line); vertical-align: top; }
tr:last-child td { border-bottom: 0; }
th { color: var(--dim); font-weight: 600; font-size: 12px; white-space: nowrap; }
dl { margin: 0; padding: 10px 14px; display: grid; grid-template-columns: max-content 1fr; gap: 4px 16px; }
dt { color: var(--dim); }
dd { margin: 0; word-break: break-all; }
.chip { display: inline-block; padding: 0 8px; margin: 1px 4px 1px 0; border-radius: 10px; background: var(--chip); font-size: 12px; }
.chips { padding: 10px 14px; }
.none, .empty { color: var(--dim); }
.empty { padding: 10px 14px; margin: 0; }
code { font: 12px ui-monospace, Menlo, monospace; word-break: break-all; }
</style>
</head>
<body>
<header>
<span class="tag">twin</span><h1>{{.Twin}}</h1>
<a href="/_twin/state">state JSON</a>
<span class="live" id="live">live, every 2s</span>
</header>
<main data-twin="{{.Twin}}" data-updated="{{.UpdatedAt}}">
{{- if .Settings}}
<section data-section="settings"><h2>Settings</h2><dl>
{{- range .Settings}}<dt>{{.Key}}</dt><dd data-key="{{.Key}}">{{.Val}}</dd>{{end}}
</dl></section>
{{- end}}
{{- range .Sections}}
<section data-section="{{.Key}}"><h2>{{.Title}} <span class="count">{{.Count}}</span></h2>
{{- if .Table}}
<div class="scroll"><table><thead><tr>{{range .Table.Columns}}<th>{{.}}</th>{{end}}</tr></thead>
<tbody>{{range .Table.Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table></div>
{{- else if .Chips}}
<div class="chips">{{range .Chips}}<span class="chip">{{.}}</span>{{end}}</div>
{{- else if .Pairs}}
<dl>{{range .Pairs}}<dt>{{.Key}}</dt><dd>{{.Val}}</dd>{{end}}</dl>
{{- else}}
<p class="empty">none</p>
{{- end}}
</section>
{{- end}}
<p class="none">updated {{.UpdatedAt}} UTC</p>
</main>
<script>
// Swap <main> with a fresh render. A failed fetch keeps the last one and
// says so in the header.
const live = document.getElementById('live');
setInterval(async () => {
  try {
    const resp = await fetch(location.href, { cache: 'no-store' });
    if (!resp.ok) throw new Error('status ' + resp.status);
    const doc = new DOMParser().parseFromString(await resp.text(), 'text/html');
    document.querySelector('main').replaceWith(doc.querySelector('main'));
    live.textContent = 'live, every 2s';
    live.classList.remove('stale');
  } catch (err) {
    live.textContent = 'twin unreachable (' + err.message + '), retrying';
    live.classList.add('stale');
  }
}, 2000);
</script>
</body>
</html>
`))
