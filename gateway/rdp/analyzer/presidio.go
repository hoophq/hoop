// Package analyzer implements async PII detection for RDP session recordings.
package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// presidioTimeout is the HTTP timeout for Presidio API calls.
const presidioTimeout = 30 * time.Second

// PresidioClient is a lightweight HTTP client for the Presidio Analyzer API.
// It only supports the /analyze endpoint (no anonymization needed).
type PresidioClient struct {
	analyzerURL string
	httpClient  *http.Client
}

// AnalyzerRequest is the request payload for Presidio's /analyze endpoint.
type AnalyzerRequest struct {
	Text           string   `json:"text"`
	Language       string   `json:"language"`
	ScoreThreshold float64  `json:"score_threshold,omitempty"`
	Entities       []string `json:"entities,omitempty"`
}

// AnalyzerResult is a single PII entity found by Presidio.
type AnalyzerResult struct {
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
	EntityType string  `json:"entity_type"`
}

// NewPresidioClient creates a new Presidio analyzer client.
// analyzerURL is the base URL for the Presidio Analyzer service (e.g. "http://localhost:5001").
func NewPresidioClient(analyzerURL string) *PresidioClient {
	return &PresidioClient{
		analyzerURL: strings.TrimSuffix(analyzerURL, "/"),
		httpClient: &http.Client{
			Timeout: presidioTimeout,
		},
	}
}

// Analyze sends text to Presidio for PII detection and returns the results.
// scoreThreshold controls the minimum confidence score (0.0-1.0). Use 0 for default (0.5).
// Returns nil (no error) with empty results if no PII is found.
func (c *PresidioClient) Analyze(ctx context.Context, text string, scoreThreshold ...float64) ([]AnalyzerResult, error) {
	if text == "" {
		return nil, nil
	}

	threshold := 0.5
	if len(scoreThreshold) > 0 && scoreThreshold[0] > 0 {
		threshold = scoreThreshold[0]
	}

	reqBody := AnalyzerRequest{
		Text:           text,
		Language:       "en",
		ScoreThreshold: threshold,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("presidio: failed to marshal request: %w", err)
	}

	apiURL := c.analyzerURL + "/analyze"
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("presidio: failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("presidio: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("presidio: failed to read response (status=%d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("presidio: analyzer returned status %d: %s", resp.StatusCode, string(body))
	}

	var results []AnalyzerResult
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("presidio: failed to decode response: %w", err)
	}

	return results, nil
}

// AggregateResults counts PII entities by type from a slice of AnalyzerResults.
// Returns a map of entity_type -> count.
func AggregateResults(results []AnalyzerResult) map[string]int64 {
	counts := make(map[string]int64)
	for _, r := range results {
		counts[r.EntityType]++
	}
	return counts
}
