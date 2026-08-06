package policy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/policy"
)

func httpStmt(d *hoopinspect.HTTPDetail) hoopinspect.Statement {
	dir := hoopinspect.FromClient
	if d.StatusCode != 0 {
		dir = hoopinspect.FromServer
	}
	return hoopinspect.Statement{
		Protocol:  hoopinspect.HTTP,
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
		v := rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{
			Method: "GET", Resource: res,
		}))
		if !v.Denied {
			t.Errorf("%s was allowed", res)
		}
		if v.Message != "the SSN endpoint is not reachable through this proxy" {
			t.Errorf("Message = %q", v.Message)
		}
	}

	if rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{
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
		if !rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{Resource: res})).Denied {
			t.Errorf("%s was allowed by /admin/**", res)
		}
	}
	if rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{Resource: "/administration"})).Denied {
		t.Error("/administration matched /admin/**; prefix matching must respect segment boundaries")
	}
}

func TestHTTPResourceMethodNarrowing(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-writes", Type: policy.MatchHTTPResource, Message: "read only"}.
			WithResources("/users/**").WithMethods("POST", "DELETE"),
	})

	if !rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{
		Method: "DELETE", Resource: "/users/*",
	})).Denied {
		t.Error("DELETE was allowed")
	}
	if rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{
		Method: "GET", Resource: "/users/*",
	})).Denied {
		t.Error("GET was denied by a POST/DELETE rule")
	}
}

// Envoy's ext_authz cannot express this rule: it decides before the upstream
// is called, so it never sees a status.
func TestHTTPStatusRule(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-5xx", Type: policy.MatchHTTPStatus,
			Message: "upstream error suppressed"}.WithStatuses("5xx"),
	})

	if !rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{StatusCode: 503})).Denied {
		t.Error("503 was allowed by a 5xx rule")
	}
	if rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{StatusCode: 200})).Denied {
		t.Error("200 was denied by a 5xx rule")
	}
	// A request statement has no status and never matches.
	if rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{Method: "GET", Resource: "/x"})).Denied {
		t.Error("a request matched a status rule")
	}
}

func TestHTTPStatusExactCode(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-404", Type: policy.MatchHTTPStatus}.WithStatuses("404"),
	})
	if !rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{StatusCode: 404})).Denied {
		t.Error("404 not matched")
	}
	if rules.Evaluate(httpStmt(&hoopinspect.HTTPDetail{StatusCode: 403})).Denied {
		t.Error("403 matched a 404 rule")
	}
}

func TestHTTPRulesIgnoreNonHTTPStatements(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
			WithResources("/admin/**"),
	})

	sql := hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Direction: hoopinspect.FromClient,
		Text:      "SELECT 1",
		Operation: hoopinspect.OpSelect,
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
			Operations: []hoopinspect.Operation{hoopinspect.OpDrop},
			Message:    "no DROP"},
		policy.Rule{Name: "no-admin-api", Type: policy.MatchHTTPResource,
			Message: "no admin API"}.WithResources("/admin/**"),
	})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	sqlDrop := hoopinspect.Statement{
		Protocol: hoopinspect.Postgres, Operation: hoopinspect.OpDrop,
		Text: "DROP TABLE t",
	}
	if v := rules.Evaluate(sqlDrop); !v.Denied || v.Rule != "no-sql-drop" {
		t.Errorf("SQL DROP: %+v", v)
	}

	apiAdmin := httpStmt(&hoopinspect.HTTPDetail{Resource: "/admin/users"})
	if v := rules.Evaluate(apiAdmin); !v.Denied || v.Rule != "no-admin-api" {
		t.Errorf("admin API: %+v", v)
	}

	sqlSelect := hoopinspect.Statement{
		Protocol: hoopinspect.Postgres, Operation: hoopinspect.OpSelect,
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
	c.Evaluate(httpStmt(&hoopinspect.HTTPDetail{
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

	(&policy.OPAClient{URL: srv.URL}).Evaluate(hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Operation: hoopinspect.OpSelect,
		Text:      "SELECT 1",
	})

	input := got["input"].(map[string]any)
	if _, present := input["http"]; present {
		t.Error("input.http present on a SQL statement")
	}
}
