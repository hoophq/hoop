package vertex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/analyzer/gemini"
)

// The google publisher carries the thinking tier, the sampling parameters
// and the billing labels on generateContent.
func TestGooglePublisherSendsThinkingSamplingAndLabels(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[
		  {"functionCall":{"name":"report_low_risk","args":{"title":"t","explanation":"e"}}}
		]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	temp := 0.0
	p := build(t, analyzer.Options{
		Endpoint: srv.URL,
		Sampling: analyzer.Sampling{Temperature: &temp},
		Extra: map[string]string{
			KeyPublisher:            PublisherGoogle,
			gemini.KeyThinkingLevel: "low",
			gemini.KeyLabels:        "team=platform, env=prod",
		},
	})
	withToken(p, "tok")
	if _, err := p.Classify(context.Background(), "s", "c"); err != nil {
		t.Fatal(err)
	}

	var body struct {
		GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
		Labels           map[string]string          `json:"labels"`
	}
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	if got := string(body.GenerationConfig["thinkingConfig"]); got != `{"thinkingLevel":"LOW"}` {
		t.Errorf("thinkingConfig = %s", got)
	}
	if got := string(body.GenerationConfig["temperature"]); got != "0" {
		t.Errorf("temperature = %s, want 0", got)
	}
	if body.Labels["team"] != "platform" || body.Labels["env"] != "prod" || len(body.Labels) != 2 {
		t.Errorf("labels = %v", body.Labels)
	}
}

// What only one publisher's API carries is refused on the others, where it
// would do nothing while reading as set.
func TestPublisherSpecificSettingsAreRefusedElsewhere(t *testing.T) {
	seed, topK := int64(1), 5
	for name, tc := range map[string]struct {
		publisher string
		extra     map[string]string
		sampling  analyzer.Sampling
		want      string
	}{
		"thinking on claude":   {PublisherAnthropic, map[string]string{gemini.KeyThinkingLevel: "low"}, analyzer.Sampling{}, "thinking_level applies to publisher"},
		"budget on open model": {PublisherOpenAPI, map[string]string{gemini.KeyThinkingBudget: "0"}, analyzer.Sampling{}, "thinking_budget applies to publisher"},
		"labels on claude":     {PublisherAnthropic, map[string]string{gemini.KeyLabels: "a=b"}, analyzer.Sampling{}, "labels applies to publisher"},
		"seed on claude":       {PublisherAnthropic, nil, analyzer.Sampling{Seed: &seed}, "no [seed] parameter"},
		"top_k on open model":  {PublisherOpenAPI, nil, analyzer.Sampling{TopK: &topK}, "no [top_k] parameter"},
	} {
		extra := map[string]string{KeyProject: "p", KeyRegion: "r", KeyPublisher: tc.publisher}
		for k, v := range tc.extra {
			extra[k] = v
		}
		_, err := analyzer.NewProvider(Name, analyzer.Options{Model: "meta/m", Extra: extra, Sampling: tc.sampling})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want containing %q", name, err, tc.want)
		}
	}
}

// Vertex rejects a malformed label on every call, so it fails the config.
func TestMalformedLabelsAreRefused(t *testing.T) {
	for _, raw := range []string{
		"team",                         // no value
		"Team=x",                       // uppercase key
		"1team=x",                      // key not starting with a letter
		"team=Platform",                // uppercase value
		"team=a,team=b",                // duplicate
		"k=" + strings.Repeat("v", 64), // value too long
	} {
		_, err := analyzer.NewProvider(Name, analyzer.Options{Model: "m", Extra: map[string]string{
			KeyProject: "p", KeyRegion: "r", KeyPublisher: PublisherGoogle, gemini.KeyLabels: raw,
		}})
		if err == nil || !strings.Contains(err.Error(), "labels") {
			t.Errorf("labels %q: err = %v, want a labels error", raw, err)
		}
	}
}
