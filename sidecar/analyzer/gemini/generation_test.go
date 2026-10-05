package gemini

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// generationConfigSent runs one classification and returns the
// generationConfig object the server received, as raw JSON fields.
func generationConfigSent(t *testing.T, opts analyzer.Options) map[string]json.RawMessage {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, highRiskReply)
	}))
	t.Cleanup(srv.Close)

	opts.Model = "gemini-3.8-flash"
	opts.Endpoint = srv.URL
	opts.Credential = analyzer.NewSecret([]byte("k"))
	p, err := analyzer.NewProvider(Name, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Classify(context.Background(), "s", "c"); err != nil {
		t.Fatal(err)
	}
	var req struct {
		GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	return req.GenerationConfig
}

// A config that names nothing new sends the body it always did: only the
// output limit. A default here would change every deployed Gemini lane.
func TestUnsetSettingsSendOnlyTheOutputLimit(t *testing.T) {
	gen := generationConfigSent(t, analyzer.Options{})
	if len(gen) != 1 || string(gen["maxOutputTokens"]) != "1024" {
		t.Errorf("generationConfig = %s, want only maxOutputTokens 1024", mustJSON(gen))
	}
}

func TestSettingsReachTheGenerationConfig(t *testing.T) {
	temp, topP, topK, seed := 0.0, 0.5, 20, int64(7)
	for name, tc := range map[string]struct {
		opts analyzer.Options
		want map[string]string
	}{
		"thinking level": {
			analyzer.Options{Extra: map[string]string{KeyThinkingLevel: " Low "}},
			map[string]string{"thinkingConfig": `{"thinkingLevel":"LOW"}`},
		},
		"thinking budget off": {
			analyzer.Options{Extra: map[string]string{KeyThinkingBudget: "0"}},
			map[string]string{"thinkingConfig": `{"thinkingBudget":0}`},
		},
		"sampling, temperature 0 included": {
			analyzer.Options{Sampling: analyzer.Sampling{Temperature: &temp, TopP: &topP, TopK: &topK, Seed: &seed}},
			map[string]string{"temperature": "0", "topP": "0.5", "topK": "20", "seed": "7"},
		},
	} {
		gen := generationConfigSent(t, tc.opts)
		for k, v := range tc.want {
			if string(gen[k]) != v {
				t.Errorf("%s: %s = %s, want %s", name, k, gen[k], v)
			}
		}
	}
}

// Each of these is a 400 on every call if sent, so it is refused when the
// provider is built.
func TestBadSettingsAreRefusedAtConstruction(t *testing.T) {
	big := int64(math.MaxInt32) + 1
	for name, tc := range map[string]struct {
		opts analyzer.Options
		want string
	}{
		"unknown level":       {analyzer.Options{Extra: map[string]string{KeyThinkingLevel: "fast"}}, `unknown thinking_level "fast"`},
		"budget below -1":     {analyzer.Options{Extra: map[string]string{KeyThinkingBudget: "-2"}}, "thinking_budget"},
		"budget not a number": {analyzer.Options{Extra: map[string]string{KeyThinkingBudget: "lots"}}, "thinking_budget"},
		"level and budget": {
			analyzer.Options{Extra: map[string]string{KeyThinkingLevel: "low", KeyThinkingBudget: "0"}},
			"mutually exclusive",
		},
		"seed past 32 bits": {analyzer.Options{Sampling: analyzer.Sampling{Seed: &big}}, "32-bit"},
	} {
		tc.opts.Model = "m"
		tc.opts.Credential = analyzer.NewSecret([]byte("k"))
		_, err := analyzer.NewProvider(Name, tc.opts)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want containing %q", name, err, tc.want)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
