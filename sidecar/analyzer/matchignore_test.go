package analyzer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

const restartedAt = "kubectl.kubernetes.io/restartedAt"

// restartIgnore is the item DEP-303 asks for: the annotation kubectl rollout
// restart stamps with the current second.
func restartIgnore(c *analyzer.Config) {
	c.Trigger = analyzer.Trigger{Operations: []inspect.Operation{inspect.OpPatch}}
	c.MatchIgnore = []analyzer.MatchIgnore{{
		Methods:   []string{"patch"},
		Resources: []string{"/apis/apps/v1/namespaces/*/deployments/*"},
		Fields:    [][]string{{"spec", "template", "metadata", "annotations", restartedAt}},
	}}
}

func patchStatement(resource, body string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Text:      "PATCH " + resource + "?fieldManager=kubectl-rollout",
		Operation: inspect.OpPatch,
		HTTP: &inspect.HTTPDetail{
			Method:   "PATCH",
			Path:     resource,
			Resource: resource,
			Body:     body,
		},
	}
}

func restartBody(at string) string {
	return `{"spec":{"template":{"metadata":{"annotations":{"` + restartedAt + `":"` + at + `"}}}}}`
}

const deployment = "/apis/apps/v1/namespaces/default/deployments/web"

// fileOnce runs one statement on a lane that files and leaves it pending, and
// returns the bytes filed and the verdict's annotations.
func fileOnce(t *testing.T, edit func(*analyzer.Config), stmt inspect.Statement) (string, map[string]string) {
	t.Helper()
	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}}
	v := holdingEvaluator(t, rev, func(c *analyzer.Config) {
		c.ReviewMode = analyzer.ReviewReturn
		edit(c)
	}).Evaluate(stmt)
	sent := rev.statements()
	if len(sent) != 1 {
		t.Fatalf("the review was filed %d times, want 1", len(sent))
	}
	return sent[0], v.Annotations
}

// Two restarts a second apart file the same bytes, so the resend matches the
// approval of the first. The reviewer reads which value it does not bind.
func TestAnIgnoredFieldFilesTheSameBytesAcrossRequests(t *testing.T) {
	first, notes := fileOnce(t, restartIgnore, patchStatement(deployment, restartBody("2026-10-14T10:00:00Z")))
	second, _ := fileOnce(t, restartIgnore, patchStatement(deployment, restartBody("2026-10-14T10:00:01Z")))

	if first != second {
		t.Errorf("the resend filed other bytes:\n%s\n%s", first, second)
	}
	want := `{"spec":{"template":{"metadata":{"annotations":{"` + restartedAt + `":"<ignored by match_ignore>"}}}}}`
	if !strings.HasSuffix(first, "\n\n"+want) {
		t.Errorf("the review carries %q, want the body %q", first, want)
	}
	if got := notes[analyzer.MetadataReviewMatchIgnored]; got != "1" {
		t.Errorf("%s is %q, want 1", analyzer.MetadataReviewMatchIgnored, got)
	}
}

// Only the named value is unbound. A patch that also changes the image is
// another statement, and needs its own approval.
func TestAnIgnoredFieldLeavesTheRestBound(t *testing.T) {
	image := `{"spec":{"template":{"metadata":{"annotations":{"` + restartedAt + `":"2026-10-14T10:00:00Z"}},` +
		`"spec":{"containers":[{"name":"web","image":"evil:latest"}]}}}}`
	plain, _ := fileOnce(t, restartIgnore, patchStatement(deployment, restartBody("2026-10-14T10:00:00Z")))
	changed, _ := fileOnce(t, restartIgnore, patchStatement(deployment, image))

	if plain == changed {
		t.Fatal("a patch that changes the image matched the restart")
	}
	if !strings.Contains(changed, "evil:latest") {
		t.Errorf("the review hides the image: %q", changed)
	}
}

// Outside its methods and resources an item does nothing, and the exact bytes
// are filed as before.
func TestAnIgnoreOutsideItsScopeFilesTheExactBytes(t *testing.T) {
	body := restartBody("2026-10-14T10:00:00Z")
	for _, tc := range []struct {
		name string
		stmt inspect.Statement
	}{
		{"another resource", patchStatement("/apis/apps/v1/namespaces/default/statefulsets/web", body)},
		{"another method", func() inspect.Statement {
			s := patchStatement(deployment, body)
			s.HTTP.Method = "PUT"
			return s
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent, notes := fileOnce(t, restartIgnore, tc.stmt)
			if !strings.HasSuffix(sent, "\n\n"+body) {
				t.Errorf("the review carries %q, want the exact body", sent)
			}
			if _, ok := notes[analyzer.MetadataReviewMatchIgnored]; ok {
				t.Errorf("%s is set on a request no item applies to", analyzer.MetadataReviewMatchIgnored)
			}
		})
	}
}

// The value is replaced in place. Every other byte is the client's, and a
// duplicate key is replaced at each occurrence rather than collapsed, so the
// reviewer reads what the upstream will parse.
func TestAnIgnoredFieldKeepsTheClientsBytes(t *testing.T) {
	edit := func(c *analyzer.Config) {
		c.Trigger = analyzer.Trigger{Operations: []inspect.Operation{inspect.OpPatch}}
		c.MatchIgnore = []analyzer.MatchIgnore{{
			Resources: []string{deployment},
			Fields:    [][]string{{"meta", "at"}, {"n"}},
		}}
	}
	body := "{ \"z\": 1,\n  \"meta\" : { \"at\" : \"t1\", \"at\": \"t2\" },\n  \"n\": 1.50 }"
	sent, notes := fileOnce(t, edit, patchStatement(deployment, body))

	want := "{ \"z\": 1,\n  \"meta\" : { \"at\" : \"<ignored by match_ignore>\", \"at\": \"<ignored by match_ignore>\" },\n" +
		"  \"n\": \"<ignored by match_ignore>\" }"
	if !strings.HasSuffix(sent, "\n\n"+want) {
		t.Errorf("the review carries\n%s\nwant the body\n%s", sent, want)
	}
	if got := notes[analyzer.MetadataReviewMatchIgnored]; got != "3" {
		t.Errorf("%s is %q, want 3", analyzer.MetadataReviewMatchIgnored, got)
	}
}

// Escapes do not move the replaced span. A key matches by its decoded name,
// as the upstream reads it; a number past float64 does not stop the walk.
func TestAnIgnoredFieldSurvivesEscapesAndLargeNumbers(t *testing.T) {
	edit := func(c *analyzer.Config) {
		c.Trigger = analyzer.Trigger{Operations: []inspect.Operation{inspect.OpPatch}}
		c.MatchIgnore = []analyzer.MatchIgnore{{
			Resources: []string{deployment},
			Fields:    [][]string{{"ab"}},
		}}
	}
	body := `{"note":"x\"y\\","big":1e999,"ab":"t\"1é","z":"\"ab\":1"}`
	sent, notes := fileOnce(t, edit, patchStatement(deployment, body))

	want := `{"note":"x\"y\\","big":1e999,"ab":"<ignored by match_ignore>","z":"\"ab\":1"}`
	if !strings.HasSuffix(sent, "\n\n"+want) {
		t.Errorf("the review carries\n%s\nwant the body\n%s", sent, want)
	}
	if got := notes[analyzer.MetadataReviewMatchIgnored]; got != "1" {
		t.Errorf("%s is %q, want 1", analyzer.MetadataReviewMatchIgnored, got)
	}
}

// Every item that applies adds its fields; one scoped elsewhere adds none,
// and a path two items name is replaced once.
func TestOnlyTheItemsThatApplyIgnoreFields(t *testing.T) {
	edit := func(c *analyzer.Config) {
		c.Trigger = analyzer.Trigger{Operations: []inspect.Operation{inspect.OpPatch}}
		c.MatchIgnore = []analyzer.MatchIgnore{
			{Resources: []string{"/apis/apps/v1/namespaces/*/statefulsets/*"}, Fields: [][]string{{"a"}}},
			{Methods: []string{"PATCH"}, Resources: []string{deployment}, Fields: [][]string{{"b"}}},
			{Resources: []string{"/apis/apps/v1/namespaces/*/deployments/*"}, Fields: [][]string{{"b"}}},
		}
	}
	sent, notes := fileOnce(t, edit, patchStatement(deployment, `{"a":"1","b":"2"}`))

	if want := `{"a":"1","b":"<ignored by match_ignore>"}`; !strings.HasSuffix(sent, "\n\n"+want) {
		t.Errorf("the review carries %q, want the body %q", sent, want)
	}
	if got := notes[analyzer.MetadataReviewMatchIgnored]; got != "1" {
		t.Errorf("%s is %q, want 1", analyzer.MetadataReviewMatchIgnored, got)
	}
}

// A path can end where another goes on. The object there stays bound, the
// scalar under it is replaced, and a scalar at the shorter path is replaced.
func TestNestedPathsIgnoreOnlyTheirScalars(t *testing.T) {
	edit := func(c *analyzer.Config) {
		c.Trigger = analyzer.Trigger{Operations: []inspect.Operation{inspect.OpPatch}}
		c.MatchIgnore = []analyzer.MatchIgnore{{
			Resources: []string{deployment},
			Fields:    [][]string{{"a"}, {"a", "b"}},
		}}
	}
	for body, want := range map[string]string{
		`{"a":{"b":1,"c":2}}`: `{"a":{"b":"<ignored by match_ignore>","c":2}}`,
		`{"a":"x"}`:           `{"a":"<ignored by match_ignore>"}`,
	} {
		sent, notes := fileOnce(t, edit, patchStatement(deployment, body))
		if !strings.HasSuffix(sent, "\n\n"+want) {
			t.Errorf("body %s filed %q, want %q", body, sent, want)
		}
		if got := notes[analyzer.MetadataReviewMatchIgnored]; got != "1" {
			t.Errorf("body %s: %s is %q, want 1", body, analyzer.MetadataReviewMatchIgnored, got)
		}
	}
}

// Only a scalar is ignored, and only outside arrays. An object at the path
// would let a subtree pass a review, and a body that is not JSON has no path
// at all; each is filed exactly.
func TestAnIgnoreThatCannotApplyFilesTheExactBytes(t *testing.T) {
	edit := func(c *analyzer.Config) {
		c.Trigger = analyzer.Trigger{Operations: []inspect.Operation{inspect.OpPatch}}
		c.MatchIgnore = []analyzer.MatchIgnore{{
			Resources: []string{deployment},
			Fields:    [][]string{{"meta"}, {"items", "at"}},
		}}
	}
	for _, body := range []string{
		`{"meta":{"at":"t1"}}`,
		`{"meta":["t1"]}`,
		`{"items":[{"at":"t1"}]}`,
		`meta=t1`,
		`{"meta":"t1"`,
	} {
		t.Run(body, func(t *testing.T) {
			sent, notes := fileOnce(t, edit, patchStatement(deployment, body))
			if !strings.HasSuffix(sent, "\n\n"+body) {
				t.Errorf("the review carries %q, want the exact body", sent)
			}
			if _, ok := notes[analyzer.MetadataReviewMatchIgnored]; ok {
				t.Errorf("%s is set though nothing was replaced", analyzer.MetadataReviewMatchIgnored)
			}
		})
	}
}

// An item that would ignore more than it names is refused at construction.
func TestNewRefusesAnUnscopedMatchIgnore(t *testing.T) {
	field := [][]string{{"at"}}
	for _, tc := range []struct {
		name string
		item analyzer.MatchIgnore
		want string
	}{
		{"no resources", analyzer.MatchIgnore{Fields: field}, "names no resources"},
		{"blank resource", analyzer.MatchIgnore{Resources: []string{" "}, Fields: field}, "resources[0] is empty"},
		{"blank method", analyzer.MatchIgnore{Methods: []string{""}, Resources: []string{"/a"}, Fields: field}, "methods[0] is empty"},
		{"no fields", analyzer.MatchIgnore{Resources: []string{"/a"}}, "names no fields"},
		{"empty path", analyzer.MatchIgnore{Resources: []string{"/a"}, Fields: [][]string{{}}}, "fields[0] is empty"},
		{"empty key", analyzer.MatchIgnore{Resources: []string{"/a"}, Fields: [][]string{{"a", ""}}}, "fields[0] has an empty key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := analyzer.New(analyzer.Config{
				Provider:    &stubProvider{level: analyzer.RiskHigh},
				MatchIgnore: []analyzer.MatchIgnore{tc.item},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("New answered %v, want an error naming %q", err, tc.want)
			}
		})
	}
}
