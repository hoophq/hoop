package policy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

func httpStmt(d *inspect.HTTPDetail) inspect.Statement {
	dir := inspect.FromClient
	if d.StatusCode != 0 {
		dir = inspect.FromServer
	}
	return inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: dir,
		HTTP:      d,
	}
}

func TestHTTPResourceRule(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		policy.Rule{
			Name:    "protect-ssn",
			Type:    policy.MatchHTTPResource,
			Message: "the SSN endpoint is not reachable through this proxy",
		}.WithResources("/users/*/ssn"),
	})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	// Normalization lets one rule cover every user id.
	for _, res := range []string{"/users/*/ssn"} {
		v := rules.Evaluate(httpStmt(&inspect.HTTPDetail{
			Method: "GET", Resource: res,
		}))
		if !v.Denied {
			t.Errorf("%s was allowed", res)
		}
		if v.Message != "the SSN endpoint is not reachable through this proxy" {
			t.Errorf("Message = %q", v.Message)
		}
	}

	if rules.Evaluate(httpStmt(&inspect.HTTPDetail{
		Method: "GET", Resource: "/users/*/orders",
	})).Denied {
		t.Error("an unrelated resource was denied")
	}
}

func TestHTTPResourceDoubleStar(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
			WithResources("/admin/**"),
	})

	for _, res := range []string{"/admin", "/admin/users", "/admin/users/*/roles"} {
		if !rules.Evaluate(httpStmt(&inspect.HTTPDetail{Resource: res})).Denied {
			t.Errorf("%s was allowed by /admin/**", res)
		}
	}
	if rules.Evaluate(httpStmt(&inspect.HTTPDetail{Resource: "/administration"})).Denied {
		t.Error("/administration matched /admin/**; prefix matching must respect segment boundaries")
	}

	// A wildcard segment before the trailing /** is still a wildcard: one
	// rule covers a collection in every namespace and everything under it.
	deep, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-secrets", Type: policy.MatchHTTPResource}.
			WithResources("/api/v1/namespaces/*/secrets/**"),
	})
	for _, res := range []string{
		"/api/v1/namespaces/default/secrets",
		"/api/v1/namespaces/kube-system/secrets/k3s-serving",
	} {
		if !deep.Evaluate(httpStmt(&inspect.HTTPDetail{Resource: res})).Denied {
			t.Errorf("%s was allowed by /api/v1/namespaces/*/secrets/**", res)
		}
	}
	if deep.Evaluate(httpStmt(&inspect.HTTPDetail{Resource: "/api/v1/namespaces/default/configmaps/x"})).Denied {
		t.Error("a configmap matched the secrets pattern")
	}
}

func TestHTTPResourceDoubleStarAfterWildcard(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-secrets", Type: policy.MatchHTTPResource}.
			WithResources("/api/v1/namespaces/*/secrets/**"),
	})

	for _, res := range []string{
		"/api/v1/namespaces/kube-system/secrets",
		"/api/v1/namespaces/kube-system/secrets/bootstrap-token",
	} {
		if !rules.Evaluate(httpStmt(&inspect.HTTPDetail{Resource: res})).Denied {
			t.Errorf("%s was allowed; a * before /** must match one segment", res)
		}
	}
	for _, res := range []string{
		"/api/v1/namespaces/kube-system/configmaps/foo",
		"/api/v1/namespaces/secrets",
	} {
		if rules.Evaluate(httpStmt(&inspect.HTTPDetail{Resource: res})).Denied {
			t.Errorf("%s was denied by /api/v1/namespaces/*/secrets/**", res)
		}
	}
}

func TestHTTPResourceMalformedDoubleStar(t *testing.T) {
	// "/**" is a trailing suffix, detected on the pattern as written. A
	// pattern ending in "/**/" is not that suffix, and trimming slashes
	// before looking for it would turn "/**/" into a match for everything.
	for _, pattern := range []string{"/**/", "/admin/**/"} {
		rules, err := policy.NewRules([]policy.Rule{
			policy.Rule{Name: "malformed", Type: policy.MatchHTTPResource}.
				WithResources(pattern),
		})
		if err != nil {
			t.Fatalf("NewRules(%q): %v", pattern, err)
		}
		for _, res := range []string{"/", "/admin", "/admin/users", "/users/*/ssn"} {
			if rules.Evaluate(httpStmt(&inspect.HTTPDetail{Resource: res})).Denied {
				t.Errorf("%q denied %s; a pattern ending in /**/ is not a trailing wildcard", pattern, res)
			}
		}
	}
}

func TestHTTPResourceMethodNarrowing(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-writes", Type: policy.MatchHTTPResource, Message: "read only"}.
			WithResources("/users/**").WithMethods("POST", "DELETE"),
	})

	if !rules.Evaluate(httpStmt(&inspect.HTTPDetail{
		Method: "DELETE", Resource: "/users/*",
	})).Denied {
		t.Error("DELETE was allowed")
	}
	if rules.Evaluate(httpStmt(&inspect.HTTPDetail{
		Method: "GET", Resource: "/users/*",
	})).Denied {
		t.Error("GET was denied by a POST/DELETE rule")
	}
}

// A request whose intent is in its headers: kubectl fetches a Secret's
// contents with `Accept: application/json` and its table view with
// `Accept: application/json;as=Table;...`, on the same GET. The rule names
// the header and the values that mean "the contents", scoped to one secret,
// and lets the listing through.
func TestHTTPHeaderRule(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-secret-contents", Type: policy.MatchHTTPHeader,
			Message: "reading a secret is not permitted"}.
			WithResources("/api/v1/namespaces/*/secrets/*").
			WithMethods("GET").
			WithHeaders(map[string][]string{"Accept": {"application/json", "application/yaml", `\*/\*`}}),
	})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	secret := func(accept string) inspect.Statement {
		return httpStmt(&inspect.HTTPDetail{
			Method: "GET", Resource: "/api/v1/namespaces/default/secrets/db",
			Headers: map[string]string{"accept": accept},
		})
	}

	v := rules.Evaluate(secret("application/json"))
	if !v.Denied || v.Message != "reading a secret is not permitted" {
		t.Errorf("GET secret with Accept: application/json was allowed: %+v", v)
	}
	if !rules.Evaluate(secret("Application/YAML")).Denied {
		t.Error("header values must match case-insensitively")
	}
	if rules.Evaluate(secret("application/json;as=Table;v=v1;g=meta.k8s.io,application/json")).Denied {
		t.Error("the table view was denied; a pattern without * is an exact match on the whole value")
	}
	if rules.Evaluate(httpStmt(&inspect.HTTPDetail{
		Method: "GET", Resource: "/api/v1/namespaces/default/secrets",
		Headers: map[string]string{"accept": "application/json"},
	})).Denied {
		t.Error("listing secrets was denied; resources scope the rule")
	}
	if rules.Evaluate(httpStmt(&inspect.HTTPDetail{
		Method: "GET", Resource: "/api/v1/namespaces/default/secrets/db",
	})).Denied {
		t.Error("a request without the header was denied; the codec did not capture it, so the rule cannot read it")
	}
}

// Several headers in one rule must all match; several values for one header
// are alternatives; `*` matches any run; an empty list means present.
func TestHTTPHeaderRuleCombinators(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-kubectl-delete", Type: policy.MatchHTTPHeader}.
			WithHeaders(map[string][]string{
				"kubectl-command": {"kubectl delete*", "kubectl drain"},
				"x-hoop-user":     {},
			}),
	})
	stmt := func(h map[string]string) inspect.Statement {
		return httpStmt(&inspect.HTTPDetail{Method: "DELETE", Resource: "/api/v1/namespaces/*/pods/*", Headers: h})
	}
	for name, tc := range map[string]struct {
		headers map[string]string
		denied  bool
	}{
		"delete with a user":      {map[string]string{"kubectl-command": "kubectl delete", "x-hoop-user": "alice"}, true},
		"delete flags, wildcard":  {map[string]string{"kubectl-command": "kubectl delete --all", "x-hoop-user": "alice"}, true},
		"drain, second pattern":   {map[string]string{"kubectl-command": "kubectl drain", "x-hoop-user": "bob"}, true},
		"delete without the user": {map[string]string{"kubectl-command": "kubectl delete"}, false},
		"get, not a listed value": {map[string]string{"kubectl-command": "kubectl get", "x-hoop-user": "alice"}, false},
		"prefix without wildcard": {map[string]string{"kubectl-command": "kubectl drain --force", "x-hoop-user": "alice"}, false},
	} {
		if got := rules.Evaluate(stmt(tc.headers)).Denied; got != tc.denied {
			t.Errorf("%s: denied = %v, want %v", name, got, tc.denied)
		}
	}
}

// Envoy's ext_authz cannot express this rule: it decides before the upstream
// is called, so it never sees a status.
func TestHTTPStatusRule(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-5xx", Type: policy.MatchHTTPStatus,
			Message: "upstream error suppressed"}.WithStatuses("5xx"),
	})

	if !rules.Evaluate(httpStmt(&inspect.HTTPDetail{StatusCode: 503})).Denied {
		t.Error("503 was allowed by a 5xx rule")
	}
	if rules.Evaluate(httpStmt(&inspect.HTTPDetail{StatusCode: 200})).Denied {
		t.Error("200 was denied by a 5xx rule")
	}
	// A request statement has no status and never matches.
	if rules.Evaluate(httpStmt(&inspect.HTTPDetail{Method: "GET", Resource: "/x"})).Denied {
		t.Error("a request matched a status rule")
	}
}

func TestHTTPStatusExactCode(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-404", Type: policy.MatchHTTPStatus}.WithStatuses("404"),
	})
	if !rules.Evaluate(httpStmt(&inspect.HTTPDetail{StatusCode: 404})).Denied {
		t.Error("404 not matched")
	}
	if rules.Evaluate(httpStmt(&inspect.HTTPDetail{StatusCode: 403})).Denied {
		t.Error("403 matched a 404 rule")
	}
}

func TestHTTPRulesIgnoreNonHTTPStatements(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
			WithResources("/admin/**"),
	})

	sql := inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "SELECT 1",
		Operation: inspect.OpSelect,
	}
	if v := rules.Evaluate(sql); v.Denied {
		t.Errorf("an HTTP rule denied a SQL statement: %+v", v)
	}
}

// One ordered rule set serves a deployment fronting both a database and an
// API.
func TestMixedSQLAndHTTPRuleSet(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		{Name: "no-sql-drop", Type: policy.MatchOperation,
			Operations: []inspect.Operation{inspect.OpDrop},
			Message:    "no DROP"},
		policy.Rule{Name: "no-admin-api", Type: policy.MatchHTTPResource,
			Message: "no admin API"}.WithResources("/admin/**"),
	})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	sqlDrop := inspect.Statement{
		Protocol: inspect.Postgres, Operation: inspect.OpDrop,
		Text: "DROP TABLE t",
	}
	if v := rules.Evaluate(sqlDrop); !v.Denied || v.Rule != "no-sql-drop" {
		t.Errorf("SQL DROP: %+v", v)
	}

	apiAdmin := httpStmt(&inspect.HTTPDetail{Resource: "/admin/users"})
	if v := rules.Evaluate(apiAdmin); !v.Denied || v.Rule != "no-admin-api" {
		t.Errorf("admin API: %+v", v)
	}

	sqlSelect := inspect.Statement{
		Protocol: inspect.Postgres, Operation: inspect.OpSelect,
		Text: "SELECT 1",
	}
	if rules.Evaluate(sqlSelect).Denied {
		t.Error("a plain SELECT was denied by the mixed rule set")
	}
}

func TestInvalidHTTPRulesRejected(t *testing.T) {
	cases := map[string]policy.Rule{
		"no resources": {Name: "r", Type: policy.MatchHTTPResource},
		"no statuses":  {Name: "r", Type: policy.MatchHTTPStatus},
		"bad status":   policy.Rule{Name: "r", Type: policy.MatchHTTPStatus}.WithStatuses("nope"),
		"no headers":   {Name: "r", Type: policy.MatchHTTPHeader},
		"empty header": policy.Rule{Name: "r", Type: policy.MatchHTTPHeader}.WithHeaders(map[string][]string{" ": nil}),
	}
	for name, rule := range cases {
		if _, err := policy.NewRules([]policy.Rule{rule}); err == nil {
			t.Errorf("%s: NewRules accepted an invalid rule", name)
		}
	}
}

// The HTTP detail must reach Rego, or OPA cannot use any of this.
func TestOPAInputCarriesHTTPDetail(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"allow": true}})
	}))
	defer srv.Close()

	c := &policy.OPAClient{URL: srv.URL}
	c.Evaluate(httpStmt(&inspect.HTTPDetail{
		Method:   "DELETE",
		Path:     "/users/42",
		Resource: "/users/*",
	}))

	input, ok := got["input"].(map[string]any)
	if !ok {
		t.Fatalf("no input object: %+v", got)
	}
	h, ok := input["http"].(map[string]any)
	if !ok {
		t.Fatalf("input.http missing: %+v", input)
	}
	if h["method"] != "DELETE" {
		t.Errorf("input.http.method = %v", h["method"])
	}
	if h["resource"] != "/users/*" {
		t.Errorf("input.http.resource = %v; the normalized resource must reach Rego", h["resource"])
	}
}

// A SQL statement must not carry an empty http object into the policy input.
func TestOPAInputOmitsHTTPForSQL(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"allow": true}})
	}))
	defer srv.Close()

	(&policy.OPAClient{URL: srv.URL}).Evaluate(inspect.Statement{
		Protocol:  inspect.Postgres,
		Operation: inspect.OpSelect,
		Text:      "SELECT 1",
	})

	input := got["input"].(map[string]any)
	if _, present := input["http"]; present {
		t.Error("input.http present on a SQL statement")
	}
}
