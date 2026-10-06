package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// Messages takes temperature, top_p and top_k, and has no seed: a seed
// would read as set and do nothing, so it fails the config.
func TestSamplingIsSentAndSeedRefused(t *testing.T) {
	temp, k := 0.0, 40
	body, err := json.Marshal(BuildRequest("m", 10, analyzer.Sampling{Temperature: &temp, TopK: &k}, "s", "c", false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"temperature":0,`) || !strings.Contains(string(body), `"top_k":40`) {
		t.Errorf("body = %s, want temperature 0 and top_k 40", body)
	}
	if strings.Contains(string(body), "top_p") {
		t.Errorf("body = %s: an unset top_p was sent", body)
	}

	seed := int64(1)
	_, err = analyzer.NewProvider(Name, analyzer.Options{
		Model: "m", Credential: analyzer.NewSecret([]byte("k")),
		Sampling: analyzer.Sampling{Seed: &seed},
	})
	if err == nil || !strings.Contains(err.Error(), "seed") {
		t.Errorf("seed: err = %v, want a refusal naming seed", err)
	}
}
