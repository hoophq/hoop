package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

func init() {
	RegisterBuilder(SQLBuilder{Protocol_: inspect.Postgres})
	RegisterBuilder(SQLBuilder{Protocol_: inspect.MSSQL})
	RegisterBuilder(SQLBuilder{Protocol_: inspect.MySQL})
	RegisterBuilder(SQLBuilder{Protocol_: inspect.ClickHouse})
	RegisterBuilder(MongoDBBuilder{})
	RegisterBuilder(HTTPBuilder{})
	RegisterBuilder(grpcBuilder{})
	RegisterBuilder(spannerBuilder{})
	RegisterBuilder(sshBuilder{})
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

// Build renders the request line, the normalized resource and the body.
//
// The request line carries the request-target as the client sent it, not
// the normalized resource. The resource is for policy, where /users/12345
// and /users/67890 must be one rule; the model is judging intent, and the
// literal target is part of it: a numeric id, a `?limit=100000` or an
// `?export=all` are facts the resource form throws away, and an escaped
// separator or a doubled parameter are facts the decoded Path and parsed
// Query throw away. The resource follows on its own line where it differs
// from the path, so the model also sees which segments the codec considers
// identifiers. Identifiers in the path reach the model under the same terms
// as identifiers in the body: `send: redacted` runs the detector over this
// whole text, and the prompt contract forbids quoting a literal back.
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

	var sb strings.Builder
	sb.WriteString(d.Method)
	sb.WriteString(" ")
	sb.WriteString(httpTarget(d))
	if d.Resource != "" && d.Resource != d.Path {
		sb.WriteString("\nResource: ")
		sb.WriteString(d.Resource)
	}
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

// httpTarget is the request line's target. Target is the wire form the codec
// recorded; a detail built by hand without one falls back to the decoded
// path and a canonical rendering of its query, which is the same request
// modulo escaping and parameter order.
func httpTarget(d *inspect.HTTPDetail) string {
	if d.Target != "" {
		return d.Target
	}
	path := d.Path
	if path == "" {
		path = d.Resource
	}
	if len(d.Query) == 0 {
		return path
	}
	return path + "?" + url.Values(d.Query).Encode()
}

// httpCacheKey hashes method, normalized resource, the query and body shape.
//
// Resource rather than Path is what makes this cache work: /users/12345/orders
// and /users/67890/orders are one shape, and the codec already collapsed the
// ids. The query goes in whole — names AND values, every repeat — because
// the prompt shows the model the values and the verdict may turn on one:
// `?dry_run=true` and `?dry_run=false` beside the same body are two requests,
// and folding them would hand the second the first's verdict without a
// provider call. url.Values.Encode is the canonical form: sorted by name, so
// parameter order alone never misses the cache, and escaped, so a value
// containing `&` or `=` cannot alias another query.
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
	h.Write([]byte(url.Values(d.Query).Encode()))
	h.Write([]byte{0})
	h.Write([]byte(normalizeSpace(body)))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// grpcBuilder renders only descriptor-decoded message statements. Request
// headers and response trailers carry method and status facts for local
// policy, but no payload worth sending to a model.
type grpcBuilder struct{}

func (grpcBuilder) Protocol() inspect.Protocol { return inspect.GRPC }

func (grpcBuilder) Build(stmt inspect.Statement, maxBytes int) (Content, bool) {
	if stmt.HTTP == nil {
		return Content{}, false
	}
	body := strings.TrimSpace(stmt.HTTP.Body)
	if body == "" {
		return Content{}, false
	}

	target := stmt.HTTP.Resource
	if target == "" {
		target = stmt.HTTP.Path
	}
	var sb strings.Builder
	sb.WriteString("gRPC ")
	sb.WriteString(target)
	sb.WriteString("\nDirection: ")
	sb.WriteString(string(stmt.Direction))
	if stmt.HTTP.BodyTruncated {
		sb.WriteString("\n(message truncated by the proxy)")
	}
	sb.WriteString("\n\n")
	sb.WriteString(Truncate(body, maxBytes))

	h := sha256.New()
	h.Write([]byte("grpc"))
	h.Write([]byte{0})
	h.Write([]byte(stmt.Direction))
	h.Write([]byte{0})
	h.Write([]byte(target))
	h.Write([]byte{0})
	h.Write([]byte(normalizeSpace(body)))
	return Content{
		Text:     sb.String(),
		CacheKey: hex.EncodeToString(h.Sum(nil)[:16]),
	}, true
}

// spannerBuilder renders statements from a spanner lane, which emits two
// shapes on one protocol. A statement the daemon extracted GoogleSQL from
// carries the SQL as its Text with `spanner.sql_index` in its metadata (the
// key is a literal here because analyzer cannot import daemon), and reads
// exactly like a wire-database statement: SQL-style rendering, so the model
// sees the query plus the classifier's own reading of it, and the literal-
// stripped cache key folds parameter-only variants into one verdict. Every
// other statement — request headers, trailers, messages on RPCs that carry
// no SQL — is the grpc shape and delegates to that rendering, empty-body
// skip included. Operation is the fallback signal for the split: a lane
// statement is OpCall unless SQL analysis replaced it, so a non-OpCall
// statement is an extracted one even if the metadata key were ever lost.
type spannerBuilder struct{}

func (spannerBuilder) Protocol() inspect.Protocol { return inspect.Spanner }

func (spannerBuilder) Build(stmt inspect.Statement, maxBytes int) (Content, bool) {
	if _, ok := stmt.Metadata["spanner.sql_index"]; ok || stmt.Operation != inspect.OpCall {
		return SQLBuilder{Protocol_: inspect.Spanner}.Build(stmt, maxBytes)
	}
	return grpcBuilder{}.Build(stmt, maxBytes)
}

// OperationScoped is implemented by a Builder that answers for a FIXED set
// of operations, known before any statement arrives.
//
// It exists so the config layer can refuse a trigger naming an operation the
// builder will never build content for. Without it such a rule loads, the
// trigger matches, the builder declines, and the statement is allowed with a
// skipped finding — a control that costs money when it works and says
// nothing when it does not.
//
// A builder that does not implement it declines on CONTENT instead — an
// empty body, an empty statement — which is a per-statement fact no config
// check can predict.
type OperationScoped interface {
	AnalyzableOperations() []inspect.Operation
}

// AnalyzableOperations reports the operations a protocol's builder answers
// for, and whether it is scoped to a fixed set at all. A false second return
// means every operation reaches the builder, which then decides per
// statement.
func AnalyzableOperations(p inspect.Protocol) ([]inspect.Operation, bool) {
	b, ok := BuilderFor(p)
	if !ok {
		return nil, false
	}
	scoped, ok := b.(OperationScoped)
	if !ok {
		return nil, false
	}
	return scoped.AnalyzableOperations(), true
}

// sshBuilder renders an SSH statement for classification.
//
// exec_line is the operation worth sending, and the only one this builder
// answers for. A command line is a whole instruction a model can reason
// about — "is this exfiltration", "is this a destructive administrative
// action" — which is what an ai_analysis rule is buying.
//
// The other eleven are deliberately skipped, and skipped LOUDLY in the sense
// that matters: nothing is classified, so nothing is charged and no finding
// is invented. A variable name (env_set) and a file path (sftp_*) are short,
// structural strings with no room for intent; a model asked to rate
// "/srv/data.csv" would return a guess at full price, once per path, and a
// rule written against that verdict would be acting on noise. Both are
// already better served by a pattern rule scoped with `operations`.
//
// A shell never reaches here at all: it produces no statements, because v1
// reconstructs no keystrokes (ADR-0015).
type sshBuilder struct{}

// sshAnalyzable is the operation set this builder answers for.
//
// Build tests membership here rather than naming exec_line a second time, so
// the list a config is validated against and the list the builder honours
// are one list and cannot drift.
var sshAnalyzable = []inspect.Operation{inspect.OpExecLine}

func (sshBuilder) Protocol() inspect.Protocol { return inspect.SSH }

func (sshBuilder) AnalyzableOperations() []inspect.Operation {
	return slices.Clone(sshAnalyzable)
}

func (sshBuilder) Build(stmt inspect.Statement, maxBytes int) (Content, bool) {
	if !slices.Contains(sshAnalyzable, stmt.Operation) {
		return Content{}, false
	}
	cmd := strings.TrimSpace(stmt.Text)
	if cmd == "" {
		return Content{}, false
	}

	var sb strings.Builder
	sb.WriteString("Protocol: ssh\nOperation: ")
	sb.WriteString(string(stmt.Operation))
	sb.WriteString("\n\nCommand line submitted over SSH:\n")
	sb.WriteString(Truncate(cmd, maxBytes))

	// The cache key is the command's SHAPE after whitespace normalization,
	// and nothing more is stripped. A SQL key can drop literals because the
	// classifier already read the statement's structure; a shell command
	// has no such structure, and its arguments ARE the risk — `rm -rf /tmp`
	// and `rm -rf /` differ only there.
	h := sha256.New()
	h.Write([]byte("ssh"))
	h.Write([]byte{0})
	h.Write([]byte(normalizeSpace(cmd)))
	return Content{
		Text:     sb.String(),
		CacheKey: hex.EncodeToString(h.Sum(nil)[:16]),
	}, true
}
