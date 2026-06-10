package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hoophq/hoop/gateway/rdp/analyzer"
)

// Fixture is a self-contained export of an RDP session recording, suitable
// for replaying through the PII detection pipeline without database access.
type Fixture struct {
	SessionID    string `json:"session_id"`
	CanvasWidth  int    `json:"canvas_width"`
	CanvasHeight int    `json:"canvas_height"`
	// Events is the raw session blob stream: a JSON array of
	// [timestamp, type, base64-payload] triples as written by the recorder.
	Events []json.RawMessage `json:"events"`
}

// frameEvent is a decoded bitmap event from the blob stream.
type frameEvent struct {
	Index     int
	Timestamp float64
	Bitmap    analyzer.BitmapEvent
}

func loadFixture(path string) (*Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("invalid fixture file %s: %w", path, err)
	}
	if f.CanvasWidth <= 0 || f.CanvasHeight <= 0 {
		return nil, fmt.Errorf("fixture %s has invalid canvas dimensions %dx%d", path, f.CanvasWidth, f.CanvasHeight)
	}
	return &f, nil
}

// parseEvents decodes the bitmap ("b") events from the raw blob stream,
// preserving the original event index and timestamp. Non-bitmap events
// (e.g. the handshake event) are skipped.
func parseEvents(raw []json.RawMessage) ([]frameEvent, error) {
	var out []frameEvent
	for i, rawEvent := range raw {
		var event [3]json.RawMessage
		if err := json.Unmarshal(rawEvent, &event); err != nil {
			continue
		}
		var eventType string
		if err := json.Unmarshal(event[1], &eventType); err != nil || eventType != "b" {
			continue
		}
		var timestamp float64
		if err := json.Unmarshal(event[0], &timestamp); err != nil {
			continue
		}
		var b64Str string
		if err := json.Unmarshal(event[2], &b64Str); err != nil {
			continue
		}
		bitmapJSON, err := base64.StdEncoding.DecodeString(b64Str)
		if err != nil {
			continue
		}
		var bmp analyzer.BitmapEvent
		if err := json.Unmarshal(bitmapJSON, &bmp); err != nil {
			continue
		}
		if len(bmp.Data) == 0 || bmp.Width == 0 || bmp.Height == 0 {
			continue
		}
		out = append(out, frameEvent{Index: i, Timestamp: timestamp, Bitmap: bmp})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no bitmap events found in fixture")
	}
	return out, nil
}
