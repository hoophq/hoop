package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// Chat Completions takes temperature, top_p and seed, and has no top_k: a
// top_k would read as set and do nothing, so it fails the config.
func TestSamplingIsSentAndTopKRefused(t *testing.T) {
	temp, seed := 0.0, int64(42)
	body, err := json.Marshal(BuildRequest("m", 10, analyzer.Sampling{Temperature: &temp, Seed: &seed}, "s", "c", false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"temperature":0,`) || !strings.Contains(string(body), `"seed":42`) {
		t.Errorf("body = %s, want temperature 0 and seed 42", body)
	}
	if strings.Contains(string(body), "top_p") {
		t.Errorf("body = %s: an unset top_p was sent", body)
	}

	k := 5
	_, err = analyzer.NewProvider(Name, analyzer.Options{
		Model: "m", Credential: analyzer.NewSecret([]byte("k")),
		Sampling: analyzer.Sampling{TopK: &k},
	})
	if err == nil || !strings.Contains(err.Error(), "top_k") {
		t.Errorf("top_k: err = %v, want a refusal naming top_k", err)
	}
}
