package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

func init() {
	RegisterBuilder(SQLBuilder{Protocol_: inspect.Postgres})
	RegisterBuilder(SQLBuilder{Protocol_: inspect.MSSQL})
	RegisterBuilder(SQLBuilder{Protocol_: inspect.MySQL})
	RegisterBuilder(MongoDBBuilder{})
	RegisterBuilder(HTTPBuilder{})
}

// SQLBuilder renders a SQL statement for classification.
//
// It is parameterized by protocol rather than hardcoded to Postgres so a
// second wire-database codec registers a builder without a new type.
type SQLBuilder struct {
	Protocol_ inspect.Protocol
}

// Protocol implements Builder.
func (b SQLBuilder) Protocol() inspect.Protocol { return b.Protocol_ }

// Build renders the statement text, prefixed with the structured facts the
// codec already derived.
//
// The operation and table list go in the prompt even though they are derived
// from the same text, because they are the classifier's own reading of it: a
// model that would have missed a DROP buried in a multi-statement message
// sees it named. They cost a handful of tokens.
func (b SQLBuilder) Build(stmt inspect.Statement, maxBytes int) (Content, bool) {
	text := strings.TrimSpace(stmt.Text)
	if text == "" {
		return Content{}, false
	}

	var sb strings.Builder
	sb.WriteString("Protocol: ")
	sb.WriteString(string(stmt.Protocol))
	sb.WriteString("\nOperation: ")
	sb.WriteString(string(stmt.Operation))
	if len(stmt.Tables) > 0 {
		sb.WriteString("\nTables: ")
		sb.WriteString(strings.Join(stmt.Tables, ", "))
	}
	if stmt.Database != "" {
		sb.WriteString("\nDatabase: ")
		sb.WriteString(stmt.Database)
	}
	sb.WriteString("\n\n")
	sb.WriteString(Truncate(text, maxBytes))

	return Content{
		Text:     sb.String(),
		CacheKey: sqlCacheKey(stmt),
	}, true
}

// sqlCacheKey hashes the statement SHAPE.
//
// The text is normalized for whitespace and lowercased, and numeric and
// string literals are already stripped by the codec's classifier for the
// operation it reported. What remains still contains literals in the raw
// text, so they are stripped here too: `WHERE id = 1` and `WHERE id = 2` are
// one shape, and classifying both is money spent to learn the same thing.
func sqlCacheKey(stmt inspect.Statement) string {
	shape := stripSQLLiterals(strings.ToLower(normalizeSpace(stmt.Text)))
	h := sha256.New()
	h.Write([]byte(stmt.Protocol))
	h.Write([]byte{0})
	h.Write([]byte(stmt.Operation))
	h.Write([]byte{0})
	h.Write([]byte(shape))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// stripSQLLiterals replaces quoted strings and numeric runs with a
// placeholder so two statements differing only in their parameters hash
// alike.
//
// It is a lexer, not a parser, and it does not need to be more: a false
// merge costs one cache hit on a statement whose shape is genuinely the
// same, and a false miss costs one extra classification. Neither is a
// correctness problem, because the cache never turns a block into an allow —
// it only reuses a verdict for an identical shape.
func stripSQLLiterals(s string) string {
	var out strings.Builder
	out.Grow(len(s))

	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'' || c == '"':
			quote := c
			i++
			for i < len(s) {
				if s[i] == quote {
					// A doubled quote is an escaped quote inside
					// the literal, not the end of it.
					if i+1 < len(s) && s[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			out.WriteByte('?')
		case c >= '0' && c <= '9':
			for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
				i++
			}
			out.WriteByte('?')
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}

// MongoDBBuilder renders a MongoDB command for classification.
type MongoDBBuilder struct{}

// Protocol implements Builder.
func (MongoDBBuilder) Protocol() inspect.Protocol { return inspect.MongoDB }

// Build prefixes Extended JSON with the command facts the wire codec derived.
func (MongoDBBuilder) Build(stmt inspect.Statement, maxBytes int) (Content, bool) {
	text := strings.TrimSpace(stmt.Text)
	if text == "" {
		return Content{}, false
	}

	var sb strings.Builder
	sb.WriteString("Protocol: mongodb")
	if command := stmt.Metadata["mongodb.command"]; command != "" {
		sb.WriteString("\nCommand: ")
		sb.WriteString(command)
	}
	sb.WriteString("\nOperation: ")
	sb.WriteString(string(stmt.Operation))
	if len(stmt.Tables) > 0 {
		sb.WriteString("\nCollections: ")
		sb.WriteString(strings.Join(stmt.Tables, ", "))
	}
	if stmt.Database != "" {
		sb.WriteString("\nDatabase: ")
		sb.WriteString(stmt.Database)
	}
	sb.WriteString("\n\n")
	sb.WriteString(Truncate(text, maxBytes))

	return Content{
		Text:     sb.String(),
		CacheKey: mongodbCacheKey(stmt),
	}, true
}

// mongodbCacheKey hashes command structure rather than values. Two find
// commands that differ only in a filter literal need one model call, while
// different field names, commands and collections remain distinct.
func mongodbCacheKey(stmt inspect.Statement) string {
	h := sha256.New()
	h.Write([]byte(stmt.Protocol))
	h.Write([]byte{0})
	h.Write([]byte(stmt.Metadata["mongodb.command"]))
	h.Write([]byte{0})
	h.Write([]byte(stmt.Operation))
	h.Write([]byte{0})
	h.Write([]byte(stmt.Database))
	for _, table := range stmt.Tables {
		h.Write([]byte{0})
		h.Write([]byte(table))
	}
	h.Write([]byte{0})
	h.Write([]byte(mongodbShape(stmt.Text)))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func mongodbShape(text string) string {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return normalizeSpace(text)
	}
	shaped := mongodbShapeValue(value, "")
	encoded, err := json.Marshal(shaped)
	if err != nil {
		return normalizeSpace(text)
	}
	return string(encoded)
}

// batchFields name the arrays whose ORDER and LENGTH carry no meaning a
// policy could read: a write batch is a bag of documents, and an
// insertMany of three or of three hundred asks the same question about the
// same shape. Folding them to their distinct element shapes is what lets one
// model verdict serve every batch size.
//
// Every other array keeps its order and multiplicity, because for most of
// them the sequence IS the meaning. An aggregation pipeline is the sharp
// case: `[{$match}, {$limit}]` and `[{$limit}, {$match}]` read the same
// documents in a different order and can differ in what they expose, so
// collapsing them to one cache key would hand the second command the first
// one's verdict.
var batchFields = map[string]bool{
	"documents": true,
	"updates":   true,
	"deletes":   true,
	"ops":       true,
}

// mongodbShapeValue reduces a command to its structure. field is the key the
// value was found under, empty at the root and for array elements, and it
// decides only whether this value is a write batch.
func mongodbShapeValue(value any, field string) any {
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = mongodbShapeValue(item, key)
		}
		return out
	case []any:
		out := make([]any, 0, len(value))
		for _, item := range value {
			out = append(out, mongodbShapeValue(item, ""))
		}
		if !batchFields[field] {
			return out
		}
		unique := make(map[string]any, len(out))
		for _, shape := range out {
			encoded, _ := json.Marshal(shape)
			unique[string(encoded)] = shape
		}
		keys := make([]string, 0, len(unique))
		for key := range unique {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		folded := make([]any, 0, len(keys))
		for _, key := range keys {
			folded = append(folded, unique[key])
		}
		return folded
	case string:
		return "$string"
	case json.Number:
		return "$number"
	case bool:
		return "$bool"
	case nil:
		return nil
	default:
		return "$value"
	}
}

// HTTPBuilder renders an HTTP request for classification.
type HTTPBuilder struct{}

// Protocol implements Builder.
func (HTTPBuilder) Protocol() inspect.Protocol { return inspect.HTTP }

// Build renders "METHOD resource" plus the body.
//
// Headers are deliberately excluded even when a lane allowlists them for
// policy. An allowlist that is safe for a local rule is not automatically
// safe to hand a third-party model, and the one header anyone would want
// here is the one that must never leave.
//
// A request with no body returns ok=false. "POST /anything" with no body
// tells a model nothing, and paying for that verdict is the failure mode this
// whole package is built to avoid.
func (HTTPBuilder) Build(stmt inspect.Statement, maxBytes int) (Content, bool) {
	d := stmt.HTTP
	if d == nil {
		return Content{}, false
	}
	body := strings.TrimSpace(d.Body)
	if body == "" {
		return Content{}, false
	}

	target := d.Resource
	if target == "" {
		target = d.Path
	}

	var sb strings.Builder
	sb.WriteString(d.Method)
	sb.WriteString(" ")
	sb.WriteString(target)
	if d.ContentType != "" {
		sb.WriteString("\nContent-Type: ")
		sb.WriteString(d.ContentType)
	}
	if d.BodyTruncated {
		// The codec already cut this body. Say so, or the model reasons
		// about a JSON document that stops mid-key and reports the
		// malformation rather than the risk.
		sb.WriteString("\n(body truncated by the proxy)")
	}
	sb.WriteString("\n\n")
	sb.WriteString(Truncate(body, maxBytes))

	return Content{
		Text:     sb.String(),
		CacheKey: httpCacheKey(stmt, body),
	}, true
}

// httpCacheKey hashes method, normalized resource and body shape.
//
// Resource rather than Path is what makes this cache work: /users/12345/orders
// and /users/67890/orders are one shape, and the codec already collapsed the
// ids. The body is hashed whole, since there is no general way to strip its
// literals without knowing its content type.
func httpCacheKey(stmt inspect.Statement, body string) string {
	d := stmt.HTTP
	target := d.Resource
	if target == "" {
		target = d.Path
	}
	h := sha256.New()
	h.Write([]byte("http"))
	h.Write([]byte{0})
	h.Write([]byte(d.Method))
	h.Write([]byte{0})
	h.Write([]byte(target))
	h.Write([]byte{0})
	h.Write([]byte(normalizeSpace(body)))
	return hex.EncodeToString(h.Sum(nil)[:16])
}
