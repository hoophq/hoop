package http_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	hi "github.com/hoophq/hoopinspect/codec/http"
)

func req(t *testing.T, method, target string, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func TestRequestBasics(t *testing.T) {
	insp := hi.New(hi.Options{})
	r := req(t, "DELETE", "http://api.example.com/users/12345?force=true&tag=a&tag=b", "")

	s := insp.InspectRequest(r, nil)

	if s.Protocol != hoopinspect.HTTP {
		t.Errorf("Protocol = %q", s.Protocol)
	}
	if s.Direction != hoopinspect.FromClient {
		t.Errorf("Direction = %q", s.Direction)
	}
	if s.Operation != hoopinspect.OpDelete {
		t.Errorf("Operation = %q, want delete", s.Operation)
	}
	if s.HTTP == nil {
		t.Fatal("HTTP detail is nil")
	}
	if s.HTTP.Path != "/users/12345" {
		t.Errorf("Path = %q", s.HTTP.Path)
	}
	if s.HTTP.Resource != "/users/*" {
		t.Errorf("Resource = %q, want /users/*", s.HTTP.Resource)
	}
	if s.HTTP.Host != "api.example.com" {
		t.Errorf("Host = %q", s.HTTP.Host)
	}
	if got := s.HTTP.Query["tag"]; len(got) != 2 {
		t.Errorf("Query[tag] = %v, want two values", got)
	}
}

// Bodies and headers are NOT captured by default. Forwarding every header to
// a policy engine copies Authorization tokens into its decision log.
func TestBodyAndHeadersOptInOnly(t *testing.T) {
	r := req(t, "POST", "/x", `{"secret":"hunter2"}`)
	r.Header.Set("Authorization", "Bearer supersecret")

	off := hi.New(hi.Options{}).InspectRequest(r, []byte(`{"secret":"hunter2"}`))
	if off.HTTP.Body != "" {
		t.Error("body captured without CaptureBody")
	}
	if off.HTTP.Headers != nil {
		t.Errorf("headers captured without an allowlist: %v", off.HTTP.Headers)
	}

	on := hi.New(hi.Options{
		CaptureBody: true,
		Headers:     []string{"Content-Type"},
	}).InspectRequest(r, []byte(`{"secret":"hunter2"}`))
	if on.HTTP.Body != `{"secret":"hunter2"}` {
		t.Errorf("Body = %q", on.HTTP.Body)
	}
	if _, leaked := on.HTTP.Headers["authorization"]; leaked {
		t.Error("Authorization leaked despite not being allowlisted")
	}
	if on.HTTP.Headers["content-type"] != "application/json" {
		t.Errorf("Headers = %v", on.HTTP.Headers)
	}
}

// A truncated body must say so: a policy matching on content cannot treat a
// prefix as proof a pattern is absent.
func TestBodyTruncationIsFlagged(t *testing.T) {
	big := strings.Repeat("x", 100)
	insp := hi.New(hi.Options{CaptureBody: true, MaxBodyBytes: 10})

	s := insp.InspectRequest(req(t, "POST", "/x", big), []byte(big))
	if len(s.HTTP.Body) != 10 {
		t.Errorf("Body length = %d, want 10", len(s.HTTP.Body))
	}
	if !s.HTTP.BodyTruncated {
		t.Error("BodyTruncated not set on a truncated body")
	}
}

func TestResponseInspection(t *testing.T) {
	insp := hi.New(hi.Options{})
	r := req(t, "GET", "/users/12345/ssn", "")
	resp := &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Proto:      "HTTP/1.1",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	s := insp.InspectResponse(resp, r, []byte(`{"ssn":"123-45-6789"}`))

	if s.Direction != hoopinspect.FromServer {
		t.Errorf("Direction = %q, want server", s.Direction)
	}
	if s.HTTP.StatusCode != 200 {
		t.Errorf("StatusCode = %d", s.HTTP.StatusCode)
	}
	// The originating request's resource must ride along, so a policy can say
	// "no 200 on /users/*/ssn" in one rule.
	if s.HTTP.Resource != "/users/*/ssn" {
		t.Errorf("Resource = %q, want /users/*/ssn", s.HTTP.Resource)
	}
	if s.HTTP.Method != "GET" {
		t.Errorf("Method = %q", s.HTTP.Method)
	}
}

func TestMethodOperations(t *testing.T) {
	insp := hi.New(hi.Options{})
	for method, want := range map[string]hoopinspect.Operation{
		"GET":     hoopinspect.OpGet,
		"POST":    hoopinspect.OpPost,
		"PUT":     hoopinspect.OpPut,
		"PATCH":   hoopinspect.OpPatch,
		"DELETE":  hoopinspect.OpDelete,
		"HEAD":    hoopinspect.OpHead,
		"OPTIONS": hoopinspect.OpOptions,
	} {
		s := insp.InspectRequest(req(t, method, "/x", ""), nil)
		if s.Operation != want {
			t.Errorf("%s -> %q, want %q", method, s.Operation, want)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/users/12345", "/users/*"},
		{"/users/12345/orders/98765", "/users/*/orders/*"},
		{"/users/3f6b2c1a-4d5e-6f70-8192-a3b4c5d6e7f8", "/users/*"},
		{"/blobs/9f86d081884c7d65", "/blobs/*"},
		{"/api/v1/health", "/api/v1/health"},
		{"/", "/"},
		{"", ""},
		{"/reports/12345.pdf", "/reports/*.pdf"},
		{"/static/index.html", "/static/index.html"},

		// A short slug is NOT collapsed. Merging /users/alice with
		// /users/settings would silently widen every rule written against
		// either, so the normalizer errs toward keeping segments.
		{"/users/alice", "/users/alice"},
		{"/users/settings", "/users/settings"},
	}
	for _, tc := range tests {
		if got := hi.NormalizePath(tc.in); got != tc.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- GraphQL -------------------------------------------------------------

// The core claim: POST /graphql is one shape at the ext_authz layer, and the
// read/write distinction lives only in the body.
func TestGraphQLSeparatesReadsFromWrites(t *testing.T) {
	insp := hi.New(hi.Options{})

	read := insp.InspectRequest(
		req(t, "POST", "/graphql", `{"query":"query { user(id: 1) { name } }"}`),
		[]byte(`{"query":"query { user(id: 1) { name } }"}`))
	write := insp.InspectRequest(
		req(t, "POST", "/graphql", `{"query":"mutation { deleteUser(id: 1) }"}`),
		[]byte(`{"query":"mutation { deleteUser(id: 1) }"}`))

	// Identical at the HTTP layer.
	if read.HTTP.Method != write.HTTP.Method || read.HTTP.Path != write.HTTP.Path {
		t.Fatal("test assumption broken: the two requests should look identical")
	}
	// Distinguished by the body.
	if read.Operation != hoopinspect.OpQuery {
		t.Errorf("read Operation = %q, want query", read.Operation)
	}
	if write.Operation != hoopinspect.OpMutation {
		t.Errorf("write Operation = %q, want mutation", write.Operation)
	}
}

func TestGraphQLDetail(t *testing.T) {
	body := []byte(`{"query":"mutation Nuke($id: ID!) { deleteUser(id: $id) { id } resetPassword(id: $id) }","operationName":"Nuke"}`)
	insp := hi.New(hi.Options{})

	s := insp.InspectRequest(req(t, "POST", "/graphql", string(body)), body)
	g := s.HTTP.GraphQL
	if g == nil {
		t.Fatal("GraphQL detail is nil")
	}
	if g.OperationType != hoopinspect.OpMutation {
		t.Errorf("OperationType = %q", g.OperationType)
	}
	if g.OperationName != "Nuke" {
		t.Errorf("OperationName = %q", g.OperationName)
	}
	want := map[string]bool{"deleteUser": true, "resetPassword": true}
	if len(g.RootFields) != 2 {
		t.Fatalf("RootFields = %v, want 2", g.RootFields)
	}
	for _, f := range g.RootFields {
		if !want[f] {
			t.Errorf("unexpected root field %q", f)
		}
	}
}

// An alias must resolve to the real field, or a rule denying deleteUser is
// evaded by writing `x: deleteUser`.
func TestGraphQLAliasResolvesToRealField(t *testing.T) {
	body := []byte(`{"query":"mutation { harmless: deleteUser(id: 1) }"}`)
	s := hi.New(hi.Options{}).InspectRequest(req(t, "POST", "/graphql", string(body)), body)

	fields := s.HTTP.GraphQL.RootFields
	if len(fields) != 1 || fields[0] != "deleteUser" {
		t.Errorf("RootFields = %v, want [deleteUser] — an alias must not hide the field", fields)
	}
}

// A field name inside a string argument is data, not a selection.
func TestGraphQLStringLiteralIsNotAField(t *testing.T) {
	body := []byte(`{"query":"query { search(term: \"deleteUser\") { id } }"}`)
	s := hi.New(hi.Options{}).InspectRequest(req(t, "POST", "/graphql", string(body)), body)

	for _, f := range s.HTTP.GraphQL.RootFields {
		if f == "deleteUser" {
			t.Error("a string literal was parsed as a root field")
		}
	}
	if s.HTTP.GraphQL.OperationType != hoopinspect.OpQuery {
		t.Errorf("OperationType = %q, want query", s.HTTP.GraphQL.OperationType)
	}
}

func TestGraphQLShorthandIsAQuery(t *testing.T) {
	body := []byte(`{"query":"{ user { name } }"}`)
	s := hi.New(hi.Options{}).InspectRequest(req(t, "POST", "/graphql", string(body)), body)

	if s.HTTP.GraphQL == nil {
		t.Fatal("shorthand document not parsed")
	}
	if s.HTTP.GraphQL.OperationType != hoopinspect.OpQuery {
		t.Errorf("OperationType = %q, want query", s.HTTP.GraphQL.OperationType)
	}
}

// Fragments may precede the operation; the type must come from the operation,
// not from the first brace in the document.
func TestGraphQLFragmentBeforeOperation(t *testing.T) {
	q := `fragment F on User { id name } mutation { deleteUser(id: 1) { ...F } }`
	body := []byte(`{"query":"` + q + `"}`)
	s := hi.New(hi.Options{}).InspectRequest(req(t, "POST", "/graphql", string(body)), body)

	g := s.HTTP.GraphQL
	if g == nil {
		t.Fatal("nil GraphQL detail")
	}
	if g.OperationType != hoopinspect.OpMutation {
		t.Errorf("OperationType = %q, want mutation", g.OperationType)
	}
	if len(g.RootFields) != 1 || g.RootFields[0] != "deleteUser" {
		t.Errorf("RootFields = %v, want [deleteUser]", g.RootFields)
	}
}

func TestGraphQLDepth(t *testing.T) {
	tests := []struct {
		query string
		want  int
	}{
		{"{ a }", 1},
		{"{ a { b } }", 2},
		{"{ a { b { c { d } } } }", 4},
		{"query Q { user { friends { user { friends { id } } } } }", 5},
	}
	insp := hi.New(hi.Options{})
	for _, tc := range tests {
		body := []byte(`{"query":"` + tc.query + `"}`)
		s := insp.InspectRequest(req(t, "POST", "/graphql", string(body)), body)
		if s.HTTP.GraphQL == nil {
			t.Fatalf("%q: nil GraphQL", tc.query)
		}
		if got := s.HTTP.GraphQL.Depth; got != tc.want {
			t.Errorf("%q: Depth = %d, want %d", tc.query, got, tc.want)
		}
	}
}

// A JSON body without a query field is not GraphQL.
func TestNonGraphQLJSONIsNotParsed(t *testing.T) {
	body := []byte(`{"name":"alice","role":"admin"}`)
	s := hi.New(hi.Options{}).InspectRequest(req(t, "POST", "/api/users", string(body)), body)

	if s.HTTP.GraphQL != nil {
		t.Errorf("plain JSON parsed as GraphQL: %+v", s.HTTP.GraphQL)
	}
	if s.Operation != hoopinspect.OpPost {
		t.Errorf("Operation = %q, want post", s.Operation)
	}
}

// Batched GraphQL is rejected rather than half-inspected: reporting only the
// first operation would let the rest through unexamined.
func TestGraphQLBatchIsNotPartiallyInspected(t *testing.T) {
	body := []byte(`[{"query":"query { a }"},{"query":"mutation { deleteUser(id:1) }"}]`)
	s := hi.New(hi.Options{}).InspectRequest(req(t, "POST", "/graphql", string(body)), body)

	if s.HTTP.GraphQL != nil {
		t.Error("a batched request reported a single operation — the rest would be unexamined")
	}
}

func TestGraphQLPathRestriction(t *testing.T) {
	body := []byte(`{"query":"mutation { deleteUser(id: 1) }"}`)
	insp := hi.New(hi.Options{GraphQLPaths: []string{"/graphql"}})

	on := insp.InspectRequest(req(t, "POST", "/graphql", string(body)), body)
	if on.HTTP.GraphQL == nil {
		t.Error("configured path was not parsed")
	}
	off := insp.InspectRequest(req(t, "POST", "/other", string(body)), body)
	if off.HTTP.GraphQL != nil {
		t.Error("a non-configured path was parsed")
	}
}

func TestDisableGraphQL(t *testing.T) {
	body := []byte(`{"query":"mutation { deleteUser(id: 1) }"}`)
	s := hi.New(hi.Options{DisableGraphQL: true}).
		InspectRequest(req(t, "POST", "/graphql", string(body)), body)

	if s.HTTP.GraphQL != nil {
		t.Error("GraphQL parsed despite DisableGraphQL")
	}
	if s.Operation != hoopinspect.OpPost {
		t.Errorf("Operation = %q, want post", s.Operation)
	}
}

// --- stream decoding -----------------------------------------------------

func TestDecodeRequestStream(t *testing.T) {
	insp, err := hoopinspect.New(hoopinspect.HTTP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw := "GET /users/12345 HTTP/1.1\r\nHost: api.example.com\r\n\r\n"

	stmts, err := insp.Inspect(hoopinspect.FromClient, []byte(raw))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	if stmts[0].HTTP.Resource != "/users/*" {
		t.Errorf("Resource = %q", stmts[0].HTTP.Resource)
	}
}

func TestDecodeSplitAcrossReads(t *testing.T) {
	raw := []byte("POST /api/x HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\n\r\nhello")

	for split := 1; split < len(raw); split++ {
		insp, _ := hoopinspect.New(hoopinspect.HTTP)

		got, err := insp.Inspect(hoopinspect.FromClient, raw[:split])
		if err != nil {
			t.Fatalf("split=%d first: %v", split, err)
		}
		if len(got) != 0 {
			t.Fatalf("split=%d: emitted from a partial message", split)
		}

		got, err = insp.Inspect(hoopinspect.FromClient, raw[split:])
		if err != nil {
			t.Fatalf("split=%d second: %v", split, err)
		}
		if len(got) != 1 {
			t.Fatalf("split=%d: got %d statements, want 1", split, len(got))
		}
	}
}

func TestDecodePipelinedRequests(t *testing.T) {
	insp, _ := hoopinspect.New(hoopinspect.HTTP)
	raw := "GET /a HTTP/1.1\r\nHost: h\r\n\r\nGET /b HTTP/1.1\r\nHost: h\r\n\r\n"

	stmts, err := insp.Inspect(hoopinspect.FromClient, []byte(raw))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want 2", len(stmts))
	}
	if stmts[1].HTTP.Path != "/b" {
		t.Errorf("second Path = %q, want /b", stmts[1].HTTP.Path)
	}
}

func TestDecodeResponseStream(t *testing.T) {
	insp, _ := hoopinspect.New(hoopinspect.HTTP)
	raw := "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"

	stmts, err := insp.Inspect(hoopinspect.FromServer, []byte(raw))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	if stmts[0].HTTP.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", stmts[0].HTTP.StatusCode)
	}
	if stmts[0].Direction != hoopinspect.FromServer {
		t.Errorf("Direction = %q", stmts[0].Direction)
	}
}

func TestDecodeMalformed(t *testing.T) {
	insp, _ := hoopinspect.New(hoopinspect.HTTP)
	bad := []byte("this is not http\r\n\r\n")
	if _, err := insp.Inspect(hoopinspect.FromClient, bad); err == nil {
		t.Error("expected an error for a malformed request")
	}
}

func TestDecodeChunkedBody(t *testing.T) {
	insp, _ := hoopinspect.New(hoopinspect.HTTP)
	raw := "POST /x HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"5\r\nhello\r\n0\r\n\r\n"

	stmts, err := insp.Inspect(hoopinspect.FromClient, []byte(raw))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
}

func TestParseQueryHelper(t *testing.T) {
	q := hi.ParseQuery("a=1&a=2&b=3")
	if len(q["a"]) != 2 || q["b"][0] != "3" {
		t.Errorf("ParseQuery = %v", q)
	}
	if hi.ParseQuery("") != nil {
		t.Error("empty query should return nil")
	}
}

func TestTextRendersRequestLine(t *testing.T) {
	s := hi.New(hi.Options{}).InspectRequest(req(t, "GET", "/a?b=1", ""), nil)
	if !strings.Contains(s.Text, "GET") || !strings.Contains(s.Text, "/a") {
		t.Errorf("Text = %q", s.Text)
	}
}

func TestStatementBodyIsNotReadFromRequest(t *testing.T) {
	// InspectRequest must never consume r.Body — the caller may still need
	// it to forward upstream.
	body := "important"
	r := req(t, "POST", "/x", body)
	hi.New(hi.Options{CaptureBody: true}).InspectRequest(r, nil)

	var buf bytes.Buffer
	buf.ReadFrom(r.Body)
	if buf.String() != body {
		t.Errorf("request body was consumed: got %q, want %q", buf.String(), body)
	}
}
