package http_test

import (
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// The codec turns /reports/12345.pdf into /reports/*.pdf, so a rule naming
// that form matches. policy.ValidRulePattern must keep accepting it.
func TestANormalizedFileNameRuleStillDenies(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-pdf-reports", Type: policy.MatchHTTPResource}.WithResources("/reports/*.pdf"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for target, denied := range map[string]bool{
		"/reports/12345.pdf":  true,
		"/reports/98765.PDF":  true,
		"/reports/12345.csv":  false,
		"/reports/annual.pdf": false,
	} {
		s := decodeRequest(t, codechttp.New(codechttp.Options{}), get("api.example.com", target))
		if got := rules.Evaluate(s).Denied; got != denied {
			t.Errorf("%s: denied = %v, want %v; resource %q", target, got, denied, s.HTTP.Resource)
		}
	}
}

// A rule also matches the resource before its dot segments resolve, so a
// pattern holding one is a working rule and must keep loading.
func TestADotSegmentRuleMatchesItsUnresolvedRequest(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-escape", Type: policy.MatchHTTPResource}.WithResources("/admin/../public"),
	})
	if err != nil {
		t.Fatal(err)
	}
	s := decodeRequest(t, codechttp.New(codechttp.Options{}), get("api.example.com", "/admin/../public"))
	if s.Metadata[inspect.MetadataHTTPResourceUnresolved] != "/admin/../public" || !rules.Evaluate(s).Denied {
		t.Errorf("allowed; resource %q, unresolved %q", s.HTTP.Resource, s.Metadata[inspect.MetadataHTTPResourceUnresolved])
	}
	s = decodeRequest(t, codechttp.New(codechttp.Options{}), get("api.example.com", "/public"))
	if rules.Evaluate(s).Denied {
		t.Error("/public was denied; only the unresolved form names /admin/../public")
	}
}

// The trade-off, pinned on purpose. A client may send a literal "*" in a
// path segment, and the resource keeps it, so a rule naming /api/v1*/users
// matched that one request before. Such a rule is almost always a failed
// glob, so it is refused (ENG-1239); this test fails if either half changes.
func TestALiteralStarSegmentCanNoLongerBeNamed(t *testing.T) {
	s := decodeRequest(t, codechttp.New(codechttp.Options{}), get("api.example.com", "/api/v1*/users"))
	if s.HTTP.Resource != "/api/v1*/users" || !policy.MatchResource("/api/v1*/users", s.HTTP.Resource) {
		t.Fatalf("resource %q; want the literal path kept and matched by its own text", s.HTTP.Resource)
	}
	_, err := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "literal", Type: policy.MatchHTTPResource}.WithResources("/api/v1*/users"),
	})
	want := `literal: resource "/api/v1*/users" matches no request as written: segment "v1*"`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("NewRules = %v, want the rule refused with %q", err, want)
	}
}

// Every segment the codec collapses is a form a rule may name. This fails
// when libhoop's normalizer starts emitting a shape policy.ValidRulePattern
// refuses, which would make a working rule fail to load.
func TestRulePatternAcceptsTheCodecsNormalizedForms(t *testing.T) {
	collapsed := 0
	for _, target := range []string{
		"/r/12345",
		"/r/12345.pdf",
		"/r/12345.HTML",
		"/r/12345.a",
		"/r/12345.abcde",
		"/r/12345.abcdef",
		"/r/12345.tar.gz",
		"/r/3f6b8c1e-1d2a-4b5c-9e8f-0a1b2c3d4e5f.json",
		"/r/9f86d081884c7d65.csv",
		"/r/eyJhbGciOiJIUzI1NiIsInR5cCI6.png",
		"/r/12345.p1",
	} {
		s := decodeRequest(t, codechttp.New(codechttp.Options{}), get("api.example.com", target))
		resource := s.HTTP.Resource
		if strings.Contains(resource, "*") {
			collapsed++
		}
		if err := policy.ValidRulePattern(resource); err != nil {
			t.Errorf("%s arrives as %q, and a rule naming it is refused: %v", target, resource, err)
		}
		if !policy.MatchResource(resource, resource) {
			t.Errorf("%q does not match itself", resource)
		}
	}
	if collapsed < 5 {
		t.Fatalf("only %d targets collapsed; the inputs no longer exercise the normalizer", collapsed)
	}
}
