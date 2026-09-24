package daemon

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// Protocols lists every protocol a listener may declare in this build: the
// codec registry plus the lanes that have no codec.
func Protocols() []string {
	out := []string{string(inspect.GRPC), string(inspect.Spanner), string(inspect.SSH)}
	for _, p := range inspect.Registered() {
		out = append(out, string(p))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ConfigKeys lists every key this build decodes, as dotted paths such as
// "listeners.ssh.capabilities_allowed". A sidecar reports it on the handshake
// so the control plane refuses a key the sidecar would refuse the whole
// document over.
func ConfigKeys() []string { return slices.Clone(configKeys()) }

var configKeys = sync.OnceValue(func() []string {
	var keys []string
	var walk func(t reflect.Type, prefix string)
	walk = func(t reflect.Type, prefix string) {
		for _, f := range jsonFields(t) {
			path := joinKey(prefix, f.name)
			keys = append(keys, path)
			if st := structOf(f.typ); st != nil {
				walk(st, path)
			}
		}
	}
	walk(reflect.TypeFor[Config](), "")
	slices.Sort(keys)
	return keys
})

// DocumentKeys lists the keys cfg writes with content, in ConfigKeys' form.
// Zero scalars are left out: they read as absent to any sidecar that knows
// the key. A present pointer, slice or map counts even when empty, because
// `upstream_tls: {}` and `rules: []` are settings.
func DocumentKeys(cfg Config) []string {
	seen := map[string]bool{}
	var walk func(v reflect.Value, prefix string) bool
	walk = func(v reflect.Value, prefix string) bool {
		found := false
		for _, f := range jsonFields(v.Type()) {
			fv, err := v.FieldByIndexErr(f.index)
			if err != nil {
				continue
			}
			path := joinKey(prefix, f.name)
			if valueKeys(fv, path, walk) {
				seen[path] = true
				found = true
			}
		}
		return found
	}
	walk(reflect.ValueOf(cfg), "")
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func valueKeys(v reflect.Value, path string, walk func(reflect.Value, string) bool) bool {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return false
		}
		if v.Elem().Kind() == reflect.Struct {
			walk(v.Elem(), path)
		}
		return true
	case reflect.Slice:
		if v.IsNil() {
			return false
		}
		if structOf(v.Type()) != nil {
			for i := range v.Len() {
				if e := reflect.Indirect(v.Index(i)); e.Kind() == reflect.Struct {
					walk(e, path)
				}
			}
		}
		return true
	case reflect.Map:
		return !v.IsNil()
	case reflect.Struct:
		return walk(v, path)
	default:
		return !v.IsZero()
	}
}

type jsonField struct {
	name  string
	index []int
	typ   reflect.Type
	tag   reflect.StructTag
}

// jsonFields lists a struct's fields the way encoding/json names them,
// flattening embedded structs.
func jsonFields(t reflect.Type) []jsonField {
	var out []jsonField
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || (!f.IsExported() && !f.Anonymous) {
			continue
		}
		if name == "" && f.Anonymous {
			if st := structOf(f.Type); st != nil {
				for _, inner := range jsonFields(st) {
					inner.index = append([]int{i}, inner.index...)
					out = append(out, inner)
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, jsonField{name: name, index: []int{i}, typ: f.Type, tag: f.Tag})
	}
	return out
}

// structOf returns the struct a field holds, through pointers and lists.
func structOf(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		if t == reflect.TypeFor[json.RawMessage]() {
			return nil
		}
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		return t
	}
	return nil
}

func joinKey(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// ListenerSchema is what the control plane's listener form renders, read
// from the tags on ListenerConfig. schema.json is its committed output.
func ListenerSchema() ([]byte, error) {
	protocols := Protocols()
	doc := listenerSchema{}
	for _, p := range protocols {
		label, ok := protocolLabels[p]
		if !ok {
			return nil, fmt.Errorf("protocol %q has no label in protocolLabels", p)
		}
		doc.Protocols = append(doc.Protocols, schemaOption{Value: p, Label: label})
	}
	fields, err := schemaFields(reflect.TypeFor[ListenerConfig](), "listeners", protocols, nil)
	if err != nil {
		return nil, err
	}
	doc.Fields = fields
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

var protocolLabels = map[string]string{
	"clickhouse": "ClickHouse",
	"grpc":       "gRPC",
	"http":       "HTTP",
	"mongodb":    "MongoDB",
	"mssql":      "SQL Server",
	"mysql":      "MySQL",
	"postgres":   "PostgreSQL",
	"spanner":    "Cloud Spanner",
	"ssh":        "SSH",
}

// enumSources lets an `enum:"@name"` tag point at the list the daemon
// validates against instead of copying it.
var enumSources = map[string]func() []string{
	"ssh_capabilities": func() []string {
		out := make([]string, len(sshDeliveredCapabilities))
		for i, c := range sshDeliveredCapabilities {
			out[i] = string(c)
		}
		return out
	},
}

type listenerSchema struct {
	Protocols []schemaOption `json:"protocols"`
	Fields    []schemaField  `json:"fields"`
}

type schemaOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type schemaField struct {
	Key             string        `json:"key"`
	Type            string        `json:"type"`
	Label           string        `json:"label"`
	Help            string        `json:"help,omitempty"`
	Placeholder     string        `json:"placeholder,omitempty"`
	Required        bool          `json:"required,omitempty"`
	Basic           bool          `json:"basic,omitempty"`
	Presence        bool          `json:"presence,omitempty"`
	Enum            []string      `json:"enum,omitempty"`
	Default         string        `json:"default,omitempty"`
	Protocols       []string      `json:"protocols,omitempty"`
	ExceptProtocols []string      `json:"except_protocols,omitempty"`
	Fields          []schemaField `json:"fields,omitempty"`
}

func schemaFields(t reflect.Type, prefix string, protocols, only []string) ([]schemaField, error) {
	var out []schemaField
	var problems []string
	for _, name := range only {
		if !slices.ContainsFunc(jsonFields(t), func(f jsonField) bool { return f.name == name }) {
			problems = append(problems, fmt.Sprintf("%s: fields names %q, which %s does not have", prefix, name, t))
		}
	}
	for _, f := range jsonFields(t) {
		path := joinKey(prefix, f.name)
		if only != nil && !slices.Contains(only, f.name) {
			continue
		}
		flags := tagList(f.tag, "ui")
		if slices.Contains(flags, "-") {
			continue
		}
		sf, err := schemaFieldFor(f, path, flags, protocols)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		out = append(out, sf)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return out, nil
}

func schemaFieldFor(f jsonField, path string, flags, protocols []string) (schemaField, error) {
	sf := schemaField{
		Key:         f.name,
		Label:       f.tag.Get("label"),
		Help:        f.tag.Get("help"),
		Placeholder: f.tag.Get("placeholder"),
		Required:    slices.Contains(flags, "required"),
		Basic:       slices.Contains(flags, "basic"),
		Presence:    slices.Contains(flags, "presence"),
		Default:     f.tag.Get("default"),
	}
	if sf.Label == "" {
		return sf, fmt.Errorf(`%s: tag it label:"..." or ui:"-"`, path)
	}
	if sf.Presence && f.typ.Kind() != reflect.Pointer {
		return sf, fmt.Errorf(`%s: ui:"presence" needs a pointer field, so absent and empty differ`, path)
	}
	if ps := tagList(f.tag, "protocols"); len(ps) > 0 {
		except := strings.HasPrefix(ps[0], "!")
		ps[0] = strings.TrimPrefix(ps[0], "!")
		for _, p := range ps {
			if !slices.Contains(protocols, p) {
				return sf, fmt.Errorf("%s: protocols names %q, which this build does not speak", path, p)
			}
		}
		if except {
			sf.ExceptProtocols = ps
		} else {
			sf.Protocols = ps
		}
	}
	enum, err := tagEnum(f.tag)
	if err != nil {
		return sf, fmt.Errorf("%s: %w", path, err)
	}
	sf.Enum = enum
	if sf.Default != "" && !slices.Contains(sf.Enum, sf.Default) {
		return sf, fmt.Errorf("%s: default %q is not in enum", path, sf.Default)
	}

	t := f.typ
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t.Kind() == reflect.String:
		sf.Type = "string"
	case t.Kind() == reflect.Bool:
		sf.Type = "boolean"
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Uint64:
		sf.Type = "integer"
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.String:
		sf.Type = "list"
	case t.Kind() == reflect.Map && t.Key().Kind() == reflect.String && t.Elem().Kind() == reflect.String:
		sf.Type = "map"
	case t.Kind() == reflect.Struct:
		sf.Type = "object"
		children, err := schemaFields(t, path, protocols, tagList(f.tag, "fields"))
		if err != nil {
			return sf, err
		}
		sf.Fields = children
	default:
		return sf, fmt.Errorf(`%s: type %s has no schema type; extend ListenerSchema or tag it ui:"-"`, path, f.typ)
	}
	return sf, nil
}

func tagEnum(tag reflect.StructTag) ([]string, error) {
	values := tagList(tag, "enum")
	if len(values) == 1 && strings.HasPrefix(values[0], "@") {
		src, ok := enumSources[values[0][1:]]
		if !ok {
			return nil, fmt.Errorf("enum source %q is not in enumSources", values[0])
		}
		return src(), nil
	}
	return values, nil
}

func tagList(tag reflect.StructTag, key string) []string {
	raw := tag.Get(key)
	if raw == "" {
		return nil
	}
	var out []string
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
