package rdp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeEventLines writes a temp jsonl file with the given pre-encoded event
// lines, mirroring the format parseAndStorePDU produces.
func writeEventLines(t *testing.T, lines [][]byte) string {
	t.Helper()
	tmpPath := filepath.Join(t.TempDir(), "events.jsonl")
	f, err := os.Create(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	return tmpPath
}

func makeEventLine(t *testing.T, ts float64, payload []byte) []byte {
	t.Helper()
	line, err := json.Marshal([3]any{ts, "b", base64.StdEncoding.EncodeToString(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return line
}

// legacyEventsJSON reproduces the output of the previous whole-file builder
// so the streaming implementation can be checked for byte equivalence.
func legacyEventsJSON(handshake []byte, lines [][]byte) []byte {
	var result []byte
	result = append(result, '[')
	first := true
	if len(handshake) > 0 {
		hJSON, _ := json.Marshal([3]any{0, "h", base64.StdEncoding.EncodeToString(handshake)})
		result = append(result, hJSON...)
		first = false
	}
	for _, line := range lines {
		if !first {
			result = append(result, ',')
		}
		result = append(result, line...)
		first = false
	}
	return append(result, ']')
}

// joinChunks concatenates streamed chunks back into one JSON array, the same
// merge Postgres performs with jsonb `||` on the blob_stream column.
func joinChunks(t *testing.T, chunks []json.RawMessage) []byte {
	t.Helper()
	var merged []any
	for _, chunk := range chunks {
		var entries []any
		if err := json.Unmarshal(chunk, &entries); err != nil {
			t.Fatalf("chunk is not a valid JSON array: %v", err)
		}
		merged = append(merged, entries...)
	}
	out, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	if merged == nil {
		return []byte("[]")
	}
	return out
}

func TestStreamEventsMatchesLegacyFormat(t *testing.T) {
	payload := make([]byte, 512)
	for i := range payload {
		payload[i] = byte(i)
	}
	lines := [][]byte{
		makeEventLine(t, 0.5, payload),
		makeEventLine(t, 1.0, payload[:100]),
		makeEventLine(t, 2.25, payload[100:300]),
	}
	handshake := []byte("handshake-pdu-bytes")

	tmpPath := writeEventLines(t, lines)
	r := &RDPSessionRecorder{sessionID: "sid-equiv", handshakeData: handshake, startTime: time.Now()}

	var chunks []json.RawMessage
	entryCount, entryBytes, err := r.streamEvents(tmpPath, func(chunk json.RawMessage) error {
		chunks = append(chunks, bytes.Clone(chunk))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := len(lines) + 1; entryCount != want { // +1 handshake
		t.Fatalf("entryCount=%d want %d", entryCount, want)
	}

	legacy := legacyEventsJSON(handshake, lines)
	// Compare through a decode/encode cycle on both sides: Postgres jsonb
	// normalizes representation, so semantic equality is the contract.
	var legacyNorm []any
	if err := json.Unmarshal(legacy, &legacyNorm); err != nil {
		t.Fatal(err)
	}
	legacyJSON, _ := json.Marshal(legacyNorm)
	if got := joinChunks(t, chunks); !bytes.Equal(got, legacyJSON) {
		t.Fatalf("streamed content differs from legacy builder output\n got: %.200s\nwant: %.200s", got, legacyJSON)
	}

	// event_size arithmetic must match the legacy blob length:
	// entries + separating commas + brackets.
	if want := int64(len(legacy)); entryBytes+int64(entryCount-1)+2 != want {
		t.Fatalf("event size arithmetic: got %d want %d", entryBytes+int64(entryCount-1)+2, want)
	}
}

func TestStreamEventsChunking(t *testing.T) {
	// Lines of ~1 MiB force multiple chunks with an 8 MiB budget.
	payload := make([]byte, 768*1024)
	var lines [][]byte
	for i := range 20 { // ~20 MiB of encoded lines
		lines = append(lines, makeEventLine(t, float64(i), payload))
	}
	tmpPath := writeEventLines(t, lines)
	r := &RDPSessionRecorder{sessionID: "sid-chunks", startTime: time.Now()}

	var chunks []json.RawMessage
	total := 0
	entryCount, _, err := r.streamEvents(tmpPath, func(chunk json.RawMessage) error {
		if len(chunk) > finalizeChunkBytes+2*1024*1024 {
			return fmt.Errorf("chunk exceeds budget plus one entry: %d", len(chunk))
		}
		var entries []any
		if err := json.Unmarshal(chunk, &entries); err != nil {
			return fmt.Errorf("invalid chunk JSON: %w", err)
		}
		total += len(entries)
		chunks = append(chunks, nil) // count only
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if entryCount != len(lines) || total != len(lines) {
		t.Fatalf("entries: counted=%d sunk=%d want=%d", entryCount, total, len(lines))
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for 20 MiB of events, got %d", len(chunks))
	}
}

func TestStreamEventsEmpty(t *testing.T) {
	tmpPath := writeEventLines(t, nil)
	r := &RDPSessionRecorder{sessionID: "sid-empty", startTime: time.Now()}

	calls := 0
	entryCount, entryBytes, err := r.streamEvents(tmpPath, func(json.RawMessage) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || entryCount != 0 || entryBytes != 0 {
		t.Fatalf("empty recording should stream nothing: calls=%d count=%d bytes=%d", calls, entryCount, entryBytes)
	}
}

func TestStreamEventsSinkErrorStops(t *testing.T) {
	payload := make([]byte, 768*1024)
	var lines [][]byte
	for i := range 20 {
		lines = append(lines, makeEventLine(t, float64(i), payload))
	}
	tmpPath := writeEventLines(t, lines)
	r := &RDPSessionRecorder{sessionID: "sid-err", startTime: time.Now()}

	calls := 0
	entryCount, _, err := r.streamEvents(tmpPath, func(json.RawMessage) error {
		calls++
		if calls == 2 {
			return fmt.Errorf("db unavailable")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected sink error to propagate")
	}
	if calls != 2 {
		t.Fatalf("streaming continued after sink error: %d calls", calls)
	}
	// Only entries accepted by the sink are reported as persisted.
	if entryCount == 0 || entryCount >= len(lines) {
		t.Fatalf("entryCount=%d should reflect only the successfully sunk chunks", entryCount)
	}
}

func TestRecordOutputStopsWhenTruncated(t *testing.T) {
	r := &RDPSessionRecorder{sessionID: "sid-cap", startTime: time.Now(), truncated: true}
	r.RecordOutput(make([]byte, 4096))
	if len(r.outputBuffer) != 0 {
		t.Fatal("RecordOutput must not accumulate once the recording cap is reached")
	}
}
