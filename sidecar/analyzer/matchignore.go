package analyzer

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// MatchIgnore names JSON body fields that a review match skips, on HTTP
// requests of the given methods and resources.
//
// The control plane releases an approval only for the exact bytes it filed.
// Some clients write a value that changes on every call into an otherwise
// identical request: `kubectl rollout restart` patches
// spec.template.metadata.annotations["kubectl.kubernetes.io/restartedAt"]
// with the current second. A return-mode resend then never matches its
// approval and files a new review instead (DEP-303).
//
// The cost is that an approval no longer binds those values: the request
// that runs carries a value the reviewer never read. Hence the scope. An
// item names its resources, a field is a path of object keys, and only a
// scalar value is ignored, so the field cannot carry a subtree past a review.
type MatchIgnore struct {
	// Methods narrows the item to these methods. Empty means every method.
	Methods []string `json:"methods,omitempty"`

	// Resources are globs over the normalized resource, with the matcher an
	// http_resource rule uses. Required: an unscoped ignore would reach
	// every request on the lane.
	Resources []string `json:"resources,omitempty"`

	// Fields are paths of object keys from the body root. A list and not a
	// dotted string, because keys such as "kubectl.kubernetes.io/restartedAt"
	// carry dots. Arrays are not entered.
	Fields [][]string `json:"fields,omitempty"`
}

// matchIgnoredValue replaces an ignored value in the filed bytes. A JSON
// string, so the reviewer still reads valid JSON and sees which field the
// approval does not bind.
const matchIgnoredValue = `"<ignored by match_ignore>"`

// Validate refuses an item that would ignore more than it names.
func (m MatchIgnore) Validate() error {
	if len(m.Resources) == 0 {
		return errors.New("names no resources; an unscoped ignore reaches every request on the lane")
	}
	for i, r := range m.Resources {
		if strings.TrimSpace(r) == "" {
			return fmt.Errorf("resources[%d] is empty", i)
		}
	}
	for i, method := range m.Methods {
		if strings.TrimSpace(method) == "" {
			return fmt.Errorf("methods[%d] is empty", i)
		}
	}
	if len(m.Fields) == 0 {
		return errors.New("names no fields")
	}
	for i, path := range m.Fields {
		if len(path) == 0 {
			return fmt.Errorf("fields[%d] is empty", i)
		}
		if slices.Contains(path, "") {
			return fmt.Errorf("fields[%d] has an empty key", i)
		}
	}
	return nil
}

func (m MatchIgnore) applies(h *inspect.HTTPDetail) bool {
	if len(m.Methods) > 0 && !slices.ContainsFunc(m.Methods, func(x string) bool {
		return strings.EqualFold(x, h.Method)
	}) {
		return false
	}
	return slices.ContainsFunc(m.Resources, func(p string) bool {
		return policy.MatchResource(p, h.Resource)
	})
}

// ignoreFields returns the body to file and how many values it replaced.
func ignoreFields(h *inspect.HTTPDetail, items []MatchIgnore) (string, int) {
	var paths [][]string
	for _, m := range items {
		if m.applies(h) {
			paths = append(paths, m.Fields...)
		}
	}
	if len(paths) == 0 {
		return h.Body, 0
	}
	return ignoreJSONFields(h.Body, paths)
}

// ignoreJSONFields replaces the scalar value at each path with
// matchIgnoredValue, in place.
//
// In place, not decoded and re-encoded: re-encoding collapses duplicate keys,
// so the reviewer could read a body that the upstream parses another way.
// Every other byte is the client's. A body that is not JSON is returned as
// is, and so falls back to the exact match.
func ignoreJSONFields(body string, paths [][]string) (string, int) {
	if !json.Valid([]byte(body)) {
		return body, 0
	}
	w := jsonWalker{body: body, dec: json.NewDecoder(strings.NewReader(body))}
	// Without it a valid number past float64, such as 1e999, fails the walk
	// and the whole body falls back to the exact match.
	w.dec.UseNumber()
	if err := w.value(newKeyTree(paths)); err != nil || len(w.spans) == 0 {
		return body, 0
	}
	var b strings.Builder
	last := 0
	for _, s := range w.spans {
		b.WriteString(body[last:s[0]])
		b.WriteString(matchIgnoredValue)
		last = s[1]
	}
	b.WriteString(body[last:])
	return b.String(), len(w.spans)
}

// keyTree holds the ignored paths key by key, so each body value costs one
// map lookup however many paths are configured.
type keyTree struct {
	end  bool
	next map[string]*keyTree
}

func newKeyTree(paths [][]string) *keyTree {
	root := &keyTree{}
	for _, p := range paths {
		if len(p) == 0 {
			continue // Validate refuses it; the root would match a scalar body
		}
		n := root
		for _, k := range p {
			if n.next == nil {
				n.next = map[string]*keyTree{}
			}
			if n.next[k] == nil {
				n.next[k] = &keyTree{}
			}
			n = n.next[k]
		}
		n.end = true
	}
	return root
}

type jsonWalker struct {
	body  string
	dec   *json.Decoder
	spans [][2]int
}

// value reads one value and records its span when a path ends there. node is
// the tree at this value's path, nil where no path leads; arrays pass nil, so
// nothing inside one matches.
func (w *jsonWalker) value(node *keyTree) error {
	start := w.next()
	tok, err := w.dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('{'):
		for w.dec.More() {
			key, err := w.dec.Token()
			if err != nil {
				return err
			}
			var child *keyTree
			if k, ok := key.(string); ok && node != nil {
				child = node.next[k]
			}
			if err := w.value(child); err != nil {
				return err
			}
		}
		_, err = w.dec.Token()
		return err
	case json.Delim('['):
		for w.dec.More() {
			if err := w.value(nil); err != nil {
				return err
			}
		}
		_, err = w.dec.Token()
		return err
	}
	if node != nil && node.end {
		w.spans = append(w.spans, [2]int{start, int(w.dec.InputOffset())})
	}
	return nil
}

// next is the offset of the next value. The decoder reports the end of the
// last token, and consumes the colon or comma after it only on the next read.
func (w *jsonWalker) next() int {
	i := int(w.dec.InputOffset())
	for i < len(w.body) && strings.IndexByte(" \t\r\n:,", w.body[i]) >= 0 {
		i++
	}
	return i
}
