package sidecartui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// The listener form is built from daemon.ListenerSchema, the same document
// the control plane renders its listener form from, so a field added to
// ListenerConfig with a label reaches this screen without a change here.

type schemaField struct {
	Key             string        `json:"key"`
	Type            string        `json:"type"`
	Label           string        `json:"label"`
	Help            string        `json:"help"`
	Placeholder     string        `json:"placeholder"`
	Required        bool          `json:"required"`
	Basic           bool          `json:"basic"`
	Presence        bool          `json:"presence"`
	Open            bool          `json:"open"`
	Enum            []string      `json:"enum"`
	Default         any           `json:"default"`
	Protocols       []string      `json:"protocols"`
	ExceptProtocols []string      `json:"except_protocols"`
	Fields          []schemaField `json:"fields"`
}

func (s schemaField) appliesTo(protocol string) bool {
	if len(s.Protocols) > 0 && !slices.Contains(s.Protocols, protocol) {
		return false
	}
	return !slices.Contains(s.ExceptProtocols, protocol)
}

func loadListenerSchema() ([]schemaField, error) {
	b, err := daemon.ListenerSchema()
	if err != nil {
		return nil, err
	}
	var doc struct {
		Fields []schemaField `json:"fields"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("listener schema: %w", err)
	}
	return doc.Fields, nil
}

// listenerField ties one form field to its place in the listener document.
type listenerField struct {
	path   []string
	schema schemaField
	f      *field
	// toggle is the presence switch of a presence list (on with nothing
	// picked differs from absent there), nil otherwise.
	toggle *field
}

type listenerForm struct {
	protocol string
	form     *form
	fields   []listenerField
	// presence holds the switch of each presence object, by dotted path.
	presence map[string]*field
	advanced *field
}

// newListenerForm builds the form for protocol, with prefill keyed by
// dotted path ("upstream", "ssh.host_key"). A presence object is switched
// on by prefilling its path with "on".
//
// source says how the upstream was found on this machine, "" when the
// prefill is a default; the note at the top of the form says which.
func newListenerForm(protocol string, prefill map[string]string, source string) (*listenerForm, error) {
	schema, err := loadListenerSchema()
	if err != nil {
		return nil, err
	}
	lf := &listenerForm{protocol: protocol, presence: map[string]*field{}}
	lf.advanced = &field{id: "advanced", label: "Advanced settings", kind: fBool,
		help: "Shows every setting this protocol takes, not only the ones a first config needs."}
	var fields []*field
	var walk func(list []schemaField, path []string, prefix string, parentShown func() bool, parentRequired bool)
	walk = func(list []schemaField, path []string, prefix string, parentShown func() bool, parentRequired bool) {
		for _, s := range list {
			if s.Key == "protocol" || !s.appliesTo(protocol) {
				continue
			}
			p := append(slices.Clone(path), s.Key)
			dotted := strings.Join(p, ".")
			label := s.Label
			if prefix != "" {
				label = prefix + " › " + s.Label
			}
			essential := s.Basic || (s.Required && parentRequired)
			shown := func() bool {
				return parentShown() && (essential || lf.advanced.on)
			}
			if s.Type == "object" {
				if s.Presence {
					sw := &field{id: dotted, label: label, help: s.Help, kind: fBool, on: prefill[dotted] == "on"}
					sw.hidden = func() bool { return !parentShown() }
					lf.presence[dotted] = sw
					fields = append(fields, sw)
					walk(s.Fields, p, label, func() bool { return parentShown() && sw.on }, false)
					continue
				}
				walk(s.Fields, p, label, parentShown, s.Required && parentRequired)
				continue
			}
			f := &field{id: dotted, label: label, help: s.Help, placeholder: s.Placeholder, text: prefill[dotted]}
			if f.text == "" && s.Default != nil {
				f.text = fmt.Sprint(s.Default)
			}
			lf2 := listenerField{path: p, schema: s, f: f}
			switch {
			case s.Type == "boolean":
				f.kind, f.on = fBool, prefill[dotted] == "true"
			case s.Type == "integer":
				f.kind = fInt
			case s.Type == "string" && len(s.Enum) > 0 && !s.Open:
				f.kind = fEnum
				f.options = s.Enum
				if !s.Required && s.Default == nil {
					f.options = append([]string{""}, s.Enum...)
					f.optLabel = map[string]string{"": "not set"}
				}
				if !slices.Contains(f.options, f.text) {
					f.text = f.options[0]
				}
			case s.Type == "list" && len(s.Enum) > 0 && !s.Open:
				f.kind, f.options = fMulti, s.Enum
				f.placeholder = "none"
			case s.Type == "list":
				f.kind = fText
				if f.placeholder == "" {
					f.placeholder = "comma-separated"
				}
			case s.Type == "map":
				f.kind = fText
				f.placeholder = "key=value, key=value"
			default:
				f.kind = fText
			}
			if s.Required && f.placeholder != "" && !strings.HasPrefix(f.placeholder, "e.g.") {
				f.placeholder = "e.g. " + f.placeholder
			}
			if s.Presence && s.Type == "list" {
				sw := &field{id: dotted + "#on", label: label, help: s.Help, kind: fBool, on: prefill[dotted+"#on"] == "on"}
				sw.hidden = func() bool { return !shown() }
				f.label = label + " › pick"
				f.hidden = func() bool { return !shown() || !sw.on }
				lf2.toggle = sw
				fields = append(fields, sw)
			} else {
				f.hidden = func() bool { return !shown() }
			}
			fields = append(fields, f)
			lf.fields = append(lf.fields, lf2)
		}
	}
	walk(schema, nil, "", func() bool { return true }, true)
	// The actions lead, as on the overview: the defaults come from this
	// machine and usually fit, so Continue is where the cursor starts.
	head := []*field{
		{id: "done", label: "Continue", kind: fButton},
		{id: "back", label: "Back", kind: fButton},
		{id: "note", kind: fNote, note: func() string {
			// Says where the values came from, and asks for the ones no
			// default can fill (an ssh host key) until they are there.
			lf.form.store()
			for _, x := range lf.fields {
				if x.schema.Required && x.f.visible() && strings.TrimSpace(x.f.text) == "" && x.f.kind == fText {
					return "\n" + stText.Render("Fill in the empty fields below; ") +
						stFaint.Render("the rest has defaults you can change by moving to them and typing.") + "\n"
				}
			}
			from := "Filled in with common defaults."
			if source != "" {
				from = "Filled in from what was found: " + source + "."
			}
			note := "\n" + stFaint.Render(from+" To change a value, move to it and type.")
			if sw := lf.presence["upstream_tls"]; sw != nil && sw.on && source != "" {
				note += "\n" + stFaint.Render("Upstream TLS is on: the backend is on another machine, "+
					"so what clients send is encrypted on the way to it.")
			}
			return note + "\n"
		}},
	}
	fields = append(append(head, fields...), lf.advanced)
	lf.form = newForm("Listener", fields...)
	return lf, nil
}

// value reads the form back as a listener. A value that cannot be read (a
// number that is not one, a map entry with no "=") is an error naming the
// field, never a silent zero.
func (lf *listenerForm) value() (daemon.ListenerConfig, error) {
	lf.form.store()
	doc := map[string]any{"protocol": lf.protocol}
	set := func(path []string, v any) {
		m := doc
		for _, k := range path[:len(path)-1] {
			next, ok := m[k].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[k] = next
			}
			m = next
		}
		m[path[len(path)-1]] = v
	}
	for dotted, sw := range lf.presence {
		if sw.on && sw.visible() {
			set(strings.Split(dotted, "."), map[string]any{})
		}
	}
	for _, x := range lf.fields {
		f, s := x.f, x.schema
		if x.toggle != nil {
			if x.toggle.on && x.toggle.visible() {
				set(x.path, append([]string{}, f.multi...))
			}
			continue
		}
		if !f.visible() {
			continue
		}
		switch f.kind {
		case fBool:
			if f.on {
				set(x.path, true)
			}
		case fInt:
			if t := strings.TrimSpace(f.text); t != "" {
				n, err := strconv.Atoi(t)
				if err != nil {
					return daemon.ListenerConfig{}, fmt.Errorf("%s: %q is not a whole number", f.label, t)
				}
				set(x.path, n)
			}
		case fEnum:
			if f.text != "" {
				set(x.path, f.text)
			}
		case fMulti:
			if len(f.multi) > 0 {
				set(x.path, f.multi)
			}
		default:
			t := strings.TrimSpace(f.text)
			if t == "" {
				continue
			}
			switch s.Type {
			case "list":
				set(x.path, f.list())
			case "map":
				m := map[string]string{}
				for _, kv := range f.list() {
					k, v, ok := strings.Cut(kv, "=")
					if !ok || strings.TrimSpace(k) == "" {
						return daemon.ListenerConfig{}, fmt.Errorf("%s: %q is not key=value", f.label, kv)
					}
					m[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
				set(x.path, m)
			default:
				set(x.path, t)
			}
		}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return daemon.ListenerConfig{}, err
	}
	var l daemon.ListenerConfig
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return daemon.ListenerConfig{}, fmt.Errorf("listener: %w", err)
	}
	return l, nil
}
