package http

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/jsonsql"
	"github.com/hoophq/hoop/sidecar/lexer"
	codechttp "github.com/hoophq/libhoop/v2/codec/http"
)

// SQL in a URL parameter or a plain-text body: ClickHouse's HTTP interface
// takes it in `query`, in the POST body, or in both joined by a line feed.
// A route under http.sql.params reads the parameter and then the body; one
// under http.sql.text reads the body alone. Reading more than the server
// runs fails closed, so params always reads the body too.

// MetadataSQLInlineRows marks an INSERT whose rows follow its FORMAT clause.
// Its Text is the statement alone; the rows were not read as SQL.
const MetadataSQLInlineRows = "http.sql_inline_rows"

// contentEncoding is the header a text route needs to see. The codec
// captures it on such a lane and drops it again unless the operator listed it.
const contentEncoding = "content-encoding"

// Redacts reports whether an http lane redacts the value of the URL
// parameter name before anything reads it: the codec's default list plus
// the lane's sensitive_query_params. SQL in such a parameter cannot be read.
func Redacts(name string, extra []string) bool {
	return slices.ContainsFunc(append(codechttp.DefaultSensitiveQueryParams(), extra...),
		func(s string) bool { return strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(name)) })
}

// readText returns the SQL statement of a request to a params or text route.
func (r *sqlReader) readText(d *inspect.HTTPDetail, rt sqlRoute) ([]inspect.Statement, error) {
	var (
		param string
		found bool
	)
	if rt.source.param != "" {
		var err error
		if param, found, err = queryValue(d.Target, rt.source.param); err != nil {
			if errors.Is(err, inspect.ErrStreamUnsafe) {
				return nil, err
			}
			return r.unreadable(rt, "", err.Error()), nil
		}
	}
	hasBody := d.Body != "" || d.BodyTruncated
	if hasBody && strings.HasPrefix(strings.ToLower(d.ContentType), "multipart/") {
		// ClickHouse reads a multipart form field as a URL parameter.
		return r.unreadable(rt, param, "a multipart body can carry SQL in a form field; send the SQL in the URL or a plain body"), nil
	}
	if hasBody && rt.source.param != "" && isForm(d.ContentType) && formHas(d.Body, rt.source.param) {
		// ClickHouse reads this body as SQL; a server that parses forms reads the field.
		return r.unreadable(rt, param, fmt.Sprintf("the form body carries the SQL parameter %q, which servers read differently", rt.source.param)), nil
	}
	if found {
		if head, ok := lexer.InsertData(param, r.opts.Dialect); ok {
			return r.text(rt, head, true), nil
		}
	}
	sql := param
	if hasBody {
		if enc := d.Headers[contentEncoding]; enc != "" && !strings.EqualFold(enc, "identity") {
			return r.unreadable(rt, param, fmt.Sprintf("the body is compressed (Content-Encoding %s); a compressed body is not read", enc)), nil
		}
		if found {
			sql += "\n" + d.Body
		} else {
			sql = d.Body
		}
		// A cut right after the format name may have shortened it: FORMAT Val|ues.
		if head, ok := lexer.InsertData(sql, r.opts.Dialect); ok && (!d.BodyTruncated || len(head) < len(sql)) {
			return r.text(rt, head, true), nil
		}
		if d.BodyTruncated {
			return r.unreadable(rt, sql, "the body is larger than http.max_body_bytes; raise it"), nil
		}
	}
	if strings.TrimSpace(sql) == "" {
		return nil, nil
	}
	return r.text(rt, sql, false), nil
}

// text is the statement for SQL read from a URL parameter or a body.
func (r *sqlReader) text(rt sqlRoute, sql string, inlineRows bool) []inspect.Statement {
	if !isText(sql) {
		return r.unreadable(rt, "", "the SQL holds bytes that are not text, such as a compressed or binary body")
	}
	st := r.statement(jsonsql.SQL{Text: sql, Analysis: inspect.AnalyzeSQLIn(sql, r.opts.Dialect)}, rt, 1)
	if inlineRows {
		st.Metadata[MetadataSQLInlineRows] = "true"
	}
	return []inspect.Statement{st}
}

// unreadable is the OpUnknown statement for SQL the route should carry and
// does not yield. Text keeps only what is text.
func (r *sqlReader) unreadable(rt sqlRoute, text, reason string) []inspect.Statement {
	if !isText(text) {
		text = ""
	}
	return []inspect.Statement{r.statement(jsonsql.SQL{Text: text, Unreadable: reason}, rt, 1)}
}

// queryValue returns the value of the URL parameter name in a
// request-target. It splits on & as ClickHouse does, refuses a ; that
// changes the reading, and unescapes every key: Go's lenient parser drops a
// pair with a bad escape, which would hide the SQL.
// Names compare case-insensitively, so a server that does too cannot be
// reached past the lane. A second value is refused: reading one would
// leave the other unread.
func queryValue(target, name string) (value string, found bool, err error) {
	// RFC 9112 allows no fragment here. ClickHouse drops one and runs the
	// body alone, another server may not: either reading can hide SQL.
	if strings.IndexByte(target, '#') >= 0 {
		return "", false, fmt.Errorf("the request-target holds a #, which servers read differently: %w", inspect.ErrStreamUnsafe)
	}
	q := strings.IndexByte(target, '?')
	if q < 0 {
		return "", false, nil
	}
	query := target[q+1:]
	if value, found, err = paramValue(query, name, "&"); err != nil || strings.IndexByte(query, ';') < 0 {
		return value, found, err
	}
	// ClickHouse ends a pair at & only; libhoop and some servers also at ;.
	if v, f, err := paramValue(query, name, "&;"); err != nil || v != value || f != found {
		return "", false, fmt.Errorf("the request-target separates parameters with ;, which servers read differently: %w", inspect.ErrStreamUnsafe)
	}
	return value, found, nil
}

// paramValue is queryValue over a query string whose pairs end at any byte in seps.
func paramValue(query, name, seps string) (value string, found bool, err error) {
	for _, pair := range strings.FieldsFunc(query, func(c rune) bool { return strings.ContainsRune(seps, c) }) {
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		key, kerr := url.QueryUnescape(rawKey)
		if kerr != nil {
			return "", false, fmt.Errorf("the URL parameter %q has a malformed percent-escape", rawKey)
		}
		if !strings.EqualFold(key, name) {
			continue
		}
		if found {
			return "", false, fmt.Errorf("the request carries the SQL parameter %q twice: %w", name, inspect.ErrStreamUnsafe)
		}
		if value, err = url.QueryUnescape(rawValue); err != nil {
			return "", false, fmt.Errorf("the SQL parameter %q has a malformed percent-escape", name)
		}
		found = true
	}
	return value, found, nil
}

func isForm(contentType string) bool {
	mt, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(mt), "application/x-www-form-urlencoded")
}

func formHas(body, name string) bool {
	for _, pair := range strings.FieldsFunc(body, func(c rune) bool { return c == '&' || c == ';' }) {
		rawKey, _, _ := strings.Cut(pair, "=")
		if key, err := url.QueryUnescape(rawKey); err == nil && strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// isText reports whether s is UTF-8 with no control byte but whitespace.
// A compressed or binary body fails it, even one sent without a
// Content-Encoding (ClickHouse's decompress=1).
func isText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\t', c == '\n', c == '\v', c == '\f', c == '\r':
		case c < 0x20, c == 0x7f:
			return false
		}
	}
	return true
}
