package http_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/hoophq/hoop/hoopinspect/policy"
	hi "github.com/hoophq/libhoop/v2/codec/http"
)

func Example_resourceNormalization() {
	insp := hi.New(hi.Options{})
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "protect-ssn", Type: policy.MatchHTTPResource}.
			WithResources("/users/*/ssn").
			WithMessage("the SSN endpoint is not reachable through this proxy"),
	})

	for _, path := range []string{
		"/users/12345/ssn",
		"/users/67890/ssn",
		"/users/12345/orders",
	} {
		s := insp.InspectRequest(httptest.NewRequest("GET", path, nil), nil)
		verdict := "ALLOW"
		if rules.Evaluate(s).Denied {
			verdict = "DENY"
		}
		fmt.Printf("%-24s -> %-18s %s\n", path, s.HTTP.Resource, verdict)
	}

	// Output:
	// /users/12345/ssn         -> /users/*/ssn       DENY
	// /users/67890/ssn         -> /users/*/ssn       DENY
	// /users/12345/orders      -> /users/*/orders    ALLOW
}

// Response-side inspection, which Envoy's ext_authz cannot do: the filter
// decides before the upstream is called, so it never sees a status.
func Example_responseInspection() {
	insp := hi.New(hi.Options{})
	rules, _ := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-5xx", Type: policy.MatchHTTPStatus}.
			WithStatuses("5xx").
			WithMessage("upstream failure suppressed by policy"),
	})

	req := httptest.NewRequest("GET", "/orders/12345", nil)
	for _, code := range []int{200, 503} {
		resp := &http.Response{StatusCode: code, Header: http.Header{}}
		s := insp.InspectResponse(resp, req, nil)

		verdict := "ALLOW"
		if v := rules.Evaluate(s); v.Denied {
			verdict = "DENY: " + v.Message
		}
		fmt.Printf("%d %s -> %s\n", code, s.HTTP.Resource, verdict)
	}

	// Output:
	// 200 /orders/* -> ALLOW
	// 503 /orders/* -> DENY: upstream failure suppressed by policy
}

// Dropping this into libhoop's ReverseProxy (the `revproxy` branch).
//
// ReverseProxy already has the parsed request in inspectHandler and the
// parsed response in modifyResponse, both with the body buffered, so wiring
// this in costs one struct build per message: no re-parsing, no second copy.
//
//	// reverse_proxy_inspect.go, inside inspectHandler after the body read:
//	stmt := p.inspector.InspectRequest(r, body)
//	if v := p.policy.Evaluate(stmt); v.Denied {
//	    p.sendSessionClose(v.Message)
//	    w.Header().Set("Connection", "close")
//	    http.Error(w, v.Message, http.StatusForbidden)
//	    p.cancelFn(errors.New(v.Message))
//	    return
//	}
//
//	// reverse_proxy.go, inside modifyResponse after inspectBufferedResponse:
//	stmt := p.inspector.InspectResponse(resp, resp.Request, body)
//	if v := p.policy.Evaluate(stmt); v.Denied {
//	    return errors.New(v.Message) // routed to errorHandler -> framed 403
//	}
//
// The verdict message reaches the user through the same framed-403 path the
// branch already built for guardrail violations, so it needs no new error
// plumbing.
func Example_libhoopIntegration() {
	insp := hi.New(hi.Options{CaptureBody: true, Headers: []string{"Content-Type"}})

	body := `{"reason":"cleanup"}`
	r := httptest.NewRequest("DELETE", "https://api.internal/users/42", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer secret-token")

	s := insp.InspectRequest(r, []byte(body))

	fmt.Println("operation:", s.Operation)
	fmt.Println("resource:", s.HTTP.Resource)
	// The Authorization header is not in the allowlist, so it never reaches
	// the policy engine's decision log.
	fmt.Println("headers exposed:", s.HTTP.Headers)

	// Output:
	// operation: delete
	// resource: /users/*
	// headers exposed: map[content-type:application/json]
}
