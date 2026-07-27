package rdp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/rdp/analyzer"
	"github.com/hoophq/hoop/gateway/rdp/parser"
)

// finalizeChunkBytes bounds how much of the recorded event log is held in
// memory at a time while streaming the session blob to Postgres at close.
// Each chunk is appended to the session's blob_stream via jsonb
// concatenation, so peak finalize memory is ~one chunk instead of the whole
// recording (which for long high-throughput sessions reached GBs and
// OOM-killed the gateway).
const finalizeChunkBytes = 8 << 20 // 8 MiB

// maxRecordedBytes caps the on-disk event log per session. Once reached the
// recorder stops capturing further bitmap events (the session itself is
// unaffected); the replay is truncated rather than letting a marathon
// screen-churn session grow an unbounded temp file that must later be
// streamed to the database. Mirrors the audit WAL's flush cap philosophy
// (sessionwal.DefaultMaxRead).
const maxRecordedBytes = 128 << 20 // 128 MiB

// maxRecordedEventBytes caps a single encoded event line. Without it, one
// huge bitmap update (an uncompressed full-screen paint on a large display
// encodes at ~1.78x the pixel bytes) becomes the in-memory unit of finalize
// streaming and defeats the finalizeChunkBytes bound. Oversized events are
// skipped — losing one frame of replay — rather than truncated, since a
// partial event line would not be valid JSON. Enforced at write time, and
// again at read time as defense-in-depth for logs written by other versions.
const maxRecordedEventBytes = finalizeChunkBytes

// RDPSessionRecorder captures RDP traffic for session replay.
// Bitmap events are streamed to a temp file to avoid holding everything in memory.
type RDPSessionRecorder struct {
	mu            sync.Mutex
	sessionID     string
	orgID         string
	userID        string
	userName      string
	userEmail     string
	connection    string
	connectionSub string
	labels        map[string]string
	startTime     time.Time
	closed        bool
	// Handshake data for replay
	handshakeData []byte
	// Parser for extracting bitmaps
	parser *parser.Parser
	// Track maximum canvas dimensions seen in bitmaps
	maxWidth  uint16
	maxHeight uint16
	// Buffer for accumulating partial PDUs
	outputBuffer []byte
	// Streaming: write events to temp file instead of memory
	tmpFile       *os.File
	eventCount    int
	bitmapCount   int
	bytesWritten  int64
	lastTimestamp float64
	// truncated is set once maxRecordedBytes is reached; further output is
	// no longer parsed or recorded.
	truncated bool
}

// rdpEvent stores a single RDP event: [timestamp_seconds, event_type, base64_data]
type rdpEvent [3]any

// BitmapEvent represents a bitmap update event for storage
type BitmapEvent struct {
	X            uint16 `json:"x"`
	Y            uint16 `json:"y"`
	Width        uint16 `json:"width"`
	Height       uint16 `json:"height"`
	BitsPerPixel uint16 `json:"bits_per_pixel"`
	Compressed   bool   `json:"compressed"`
	Data         []byte `json:"data"`
}

// NewRDPSessionRecorder creates a new RDP session recorder
func NewRDPSessionRecorder(
	sessionID string,
	orgID string,
	userID string,
	userName string,
	userEmail string,
	connection string,
	connectionSub string,
) *RDPSessionRecorder {
	r := &RDPSessionRecorder{
		sessionID:     sessionID,
		orgID:         orgID,
		userID:        userID,
		userName:      userName,
		userEmail:     userEmail,
		connection:    connection,
		connectionSub: connectionSub,
		labels:        make(map[string]string),
		startTime:     time.Now().UTC(),
	}

	// Each recorder gets its own isolated parser instance. The WASM parser keeps
	// per-instance mutable state (parsed bitmaps, bump allocator, and Fast-Path
	// fragment reassembly), so sharing one instance across concurrent RDP
	// sessions corrupts that state and crashes the gateway. Use a background
	// context so a canceled request context never disables the parser mid-session.
	p, err := parser.NewParser(context.Background())
	if err != nil {
		log.With("sid", sessionID).Errorf("failed to create RDP parser, recording will store no bitmaps: %v", err)
	} else {
		r.parser = p
	}

	// Create temp file for streaming events
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("rdp-session-%s-*.jsonl", sessionID))
	if err != nil {
		log.With("sid", sessionID).Errorf("failed to create temp file for RDP recording: %v", err)
	} else {
		r.tmpFile = tmpFile
	}

	return r
}

// RecordHandshake stores the handshake response packet for replay
func (r *RDPSessionRecorder) RecordHandshake(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handshakeData = make([]byte, len(data))
	copy(r.handshakeData, data)
}

// RecordInput records client -> server traffic — skipped for RDP replay (only bitmaps matter)
func (r *RDPSessionRecorder) RecordInput(data []byte) {
	// Input events are not needed for bitmap replay, skip to save memory
}

// RecordOutput records server -> client traffic (bitmap updates, etc.)
func (r *RDPSessionRecorder) RecordOutput(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed || r.truncated || len(data) == 0 || r.parser == nil {
		return
	}

	// Append to buffer
	r.outputBuffer = append(r.outputBuffer, data...)

	// Try to parse complete PDUs from buffer
	for len(r.outputBuffer) > 0 {
		pduSize, err := r.parser.GetPduSize(r.outputBuffer)
		if err != nil {
			log.With("sid", r.sessionID).Debugf("failed to get PDU size: %v", err)
		}

		if pduSize == 0 {
			// Not a Fast-Path PDU or incomplete — skip non-FP data
			if len(r.outputBuffer) > 65536 {
				log.With("sid", r.sessionID).Debugf("discarding non-FP buffer: %d bytes", len(r.outputBuffer))
				r.outputBuffer = nil
			}
			break
		}

		if int(pduSize) > len(r.outputBuffer) {
			break
		}

		pduData := r.outputBuffer[:pduSize]
		r.outputBuffer = r.outputBuffer[pduSize:]

		r.parseAndStorePDU(pduData)
	}
}

// parseAndStorePDU parses a complete PDU and writes extracted bitmaps to disk
func (r *RDPSessionRecorder) parseAndStorePDU(data []byte) {
	if r.parser == nil || r.tmpFile == nil {
		return
	}

	timestamp := time.Since(r.startTime).Seconds()

	result, err := r.parser.Parse(data)
	if err != nil {
		log.With("sid", r.sessionID).Debugf("RDP parse error (len=%d): %v", len(data), err)
		return
	}

	if result.Error != "" {
		log.With("sid", r.sessionID).Debugf("RDP parse warning (len=%d): %s", len(data), result.Error)
	}

	if len(result.Bitmaps) == 0 {
		return
	}

	for _, bmp := range result.Bitmaps {
		bitmapData := r.parser.GetBitmapData(bmp)
		if len(bitmapData) == 0 {
			continue
		}

		// Track max dimensions
		right := bmp.X + bmp.Width
		bottom := bmp.Y + bmp.Height
		if right > r.maxWidth {
			r.maxWidth = right
		}
		if bottom > r.maxHeight {
			r.maxHeight = bottom
		}

		bitmapEvent := BitmapEvent{
			X:            bmp.X,
			Y:            bmp.Y,
			Width:        bmp.Width,
			Height:       bmp.Height,
			BitsPerPixel: bmp.BitsPerPixel,
			Compressed:   bmp.Compressed,
			Data:         bitmapData,
		}
		eventJSON, _ := json.Marshal(bitmapEvent)

		// Write as a JSON array element: [timestamp, "b", "<base64>"]
		event := [3]any{
			timestamp,
			"b",
			base64.StdEncoding.EncodeToString(eventJSON),
		}
		line, _ := json.Marshal(event)

		// Skip single events too large to stream within the finalize memory
		// bound; one lost frame beats an unbounded allocation at close.
		if len(line)+1 > maxRecordedEventBytes {
			log.With("sid", r.sessionID).Warnf(
				"skipping oversized RDP bitmap event (%d bytes encoded, cap %d); one replay frame lost",
				len(line), maxRecordedEventBytes)
			continue
		}

		// Stop recording (not the session) once the replay log reaches the
		// cap; an unbounded log would have to be streamed to the database at
		// close, and marathon screen-churn sessions produced multi-GB logs.
		if r.bytesWritten+int64(len(line))+1 > maxRecordedBytes {
			r.truncated = true
			log.With("sid", r.sessionID).Warnf(
				"RDP recording reached %d MiB cap after %d events; replay will be truncated, session continues",
				maxRecordedBytes>>20, r.eventCount)
			return
		}

		// Write to temp file (one JSON line per event)
		if _, err := r.tmpFile.Write(line); err != nil {
			log.With("sid", r.sessionID).Errorf("failed to write bitmap to temp file: %v", err)
			return
		}
		if _, err := r.tmpFile.Write([]byte("\n")); err != nil {
			log.With("sid", r.sessionID).Errorf("failed to write newline to temp file: %v", err)
			return
		}

		r.bytesWritten += int64(len(line)) + 1
		r.eventCount++
		r.bitmapCount++
		r.lastTimestamp = timestamp
	}
}

// CreateSession persists the session to the database
func (r *RDPSessionRecorder) CreateSession() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	sess := models.Session{
		ID:                r.sessionID,
		OrgID:             r.orgID,
		UserID:            r.userID,
		UserName:          r.userName,
		UserEmail:         r.userEmail,
		Connection:        r.connection,
		ConnectionType:    "custom",
		ConnectionSubtype: "rdp",
		Verb:              "connect",
		Labels:            r.labels,
		Status:            string(openapi.SessionStatusOpen),
		CreatedAt:         r.startTime,
	}

	if err := models.UpsertSession(sess); err != nil {
		log.With("sid", r.sessionID).Errorf("failed to create RDP session: %v", err)
		return err
	}

	log.With("sid", r.sessionID).Infof("RDP session created in database")
	return nil
}

// Close finalizes the session and persists all captured events
func (r *RDPSessionRecorder) Close(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return
	}
	r.closed = true

	// Flush any remaining buffered data
	if len(r.outputBuffer) > 0 {
		r.parseAndStorePDU(r.outputBuffer)
		r.outputBuffer = nil
	}

	// Release this session's isolated WASM parser instance.
	if r.parser != nil {
		_ = r.parser.Close()
		r.parser = nil
	}

	if r.tmpFile == nil {
		return
	}

	// Close temp file for writing, then stream it back. A failed close can
	// mean unflushed event data — the finalize below still runs, but the
	// replay may be truncated, so make that visible.
	tmpPath := r.tmpFile.Name()
	if closeErr := r.tmpFile.Close(); closeErr != nil {
		log.With("sid", r.sessionID).Errorf("failed closing temp recording file, replay may be incomplete: %v", closeErr)
	}
	defer os.Remove(tmpPath)

	// Stream the recorded events into the session's blob_stream in bounded
	// chunks (finalizeChunkBytes each) instead of materializing the whole
	// replay in memory. Final format in the database is identical to the
	// previous single-blob write: [ handshake_event, bitmap_event_1, ... ]
	var eventSize int64 = 2 // "[]"
	streamOK := false
	if blobErr := models.CreateEmptySessionStreamBlob(r.orgID, r.sessionID, nil); blobErr != nil {
		log.With("sid", r.sessionID).Errorf("failed creating session stream blob: %v", blobErr)
	} else {
		entryCount, entryBytes, streamErr := r.streamEvents(tmpPath, func(chunk json.RawMessage) error {
			return models.AppendSessionStream(r.orgID, r.sessionID, chunk)
		})
		if entryCount > 0 {
			// size of "[e1,e2,...]": entries + separating commas + brackets
			eventSize = entryBytes + int64(entryCount-1) + 2
		}
		if streamErr != nil {
			// Partial replay is preserved; the session is still finalized
			// below so it never gets stuck in the open state.
			log.With("sid", r.sessionID).Errorf("failed streaming session events (%d entries persisted): %v", entryCount, streamErr)
		} else {
			streamOK = true
		}
	}

	// Canvas dimensions
	canvasWidth := r.maxWidth
	canvasHeight := r.maxHeight
	if canvasWidth == 0 {
		canvasWidth = 1280
	}
	if canvasHeight == 0 {
		canvasHeight = 720
	}

	endTime := time.Now().UTC()

	var exitCode *int
	if err != nil {
		exitCode = func() *int { v := 1; return &v }()
	} else {
		exitCode = func() *int { v := 0; return &v }()
	}

	sessDone := models.SessionDone{
		ID:         r.sessionID,
		OrgID:      r.orgID,
		Metrics:    map[string]any{"event_size": eventSize, "bitmap_count": r.bitmapCount, "canvas_width": canvasWidth, "canvas_height": canvasHeight},
		Status:     string(openapi.SessionStatusDone),
		ExitCode:   exitCode,
		EndSession: &endTime,
	}

	if dbErr := models.MarkSessionDone(sessDone); dbErr != nil {
		log.With("sid", r.sessionID).Errorf("failed to finalize RDP session: %v", dbErr)
		return
	}

	// Enqueue async PII analysis if there are bitmap frames to analyze, the
	// full replay stream was persisted, the analysis pipeline is actually
	// enabled on this gateway, AND the org has the experimental flag turned
	// on. Otherwise the job would sit 'pending' forever (worker pool may be
	// stopped by the supervisor) and the session would appear stuck.
	if streamOK && r.bitmapCount > 0 &&
		analyzer.IsEnabled(appconfig.Get().MSPresidioAnalyzerURL()) &&
		featureflag.IsEnabled(r.orgID, analyzer.FlagName) {
		if enqueueErr := models.CreateRDPAnalysisJob(r.orgID, r.sessionID); enqueueErr != nil {
			log.With("sid", r.sessionID).Warnf("failed to enqueue RDP analysis job: %v", enqueueErr)
		} else {
			_ = models.UpdateSessionRDPAnalysisStatus(r.orgID, r.sessionID, "pending")
			log.With("sid", r.sessionID).Infof("enqueued RDP PII analysis job")
		}
	}

	log.With("sid", r.sessionID).Infof("RDP session finalized, events=%d, bitmaps=%d, size=%d bytes",
		r.eventCount, r.bitmapCount, eventSize)
}

// streamEvents reads the recorded events back from the temp file and hands
// them to sink as JSON array chunks of at most ~finalizeChunkBytes each
// (plus one entry of overshoot). Concatenating all chunk contents yields the
// same event sequence the previous whole-file builder produced:
// [handshake_event, bitmap_event_1, bitmap_event_2, ...]. Peak memory is one
// chunk, independent of recording size.
//
// Returns the number of entries and their cumulative encoded size (entries
// only, excluding array punctuation) that were successfully handed to the
// sink. On error, entries already accepted by the sink remain persisted —
// the caller decides how to proceed.
func (r *RDPSessionRecorder) streamEvents(tmpPath string, sink func(chunk json.RawMessage) error) (entryCount int, entryBytes int64, err error) {
	f, err := os.Open(tmpPath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to open temp file: %w", err)
	}
	defer f.Close()

	chunk := bytes.NewBuffer(make([]byte, 0, finalizeChunkBytes+(1<<20)))
	chunkEntries := 0

	flush := func() error {
		if chunkEntries == 0 {
			return nil
		}
		chunk.WriteByte(']')
		if err := sink(json.RawMessage(chunk.Bytes())); err != nil {
			return err
		}
		entryCount += chunkEntries
		entryBytes += int64(chunk.Len() - 2 - (chunkEntries - 1)) // strip brackets+commas
		chunk.Reset()
		chunkEntries = 0
		return nil
	}

	appendEntry := func(entry []byte) error {
		if chunkEntries == 0 {
			chunk.WriteByte('[')
		} else {
			chunk.WriteByte(',')
		}
		chunk.Write(entry)
		chunkEntries++
		if chunk.Len() >= finalizeChunkBytes {
			return flush()
		}
		return nil
	}

	// Handshake event goes first so replay starts from the connection setup.
	if len(r.handshakeData) > 0 {
		handshakeEvent := [3]any{
			0,
			"h",
			base64.StdEncoding.EncodeToString(r.handshakeData),
		}
		hJSON, _ := json.Marshal(handshakeEvent)
		if err := appendEntry(hJSON); err != nil {
			return entryCount, entryBytes, err
		}
	}

	// Each line in the temp file is one complete JSON event. Write-time
	// enforcement of maxRecordedEventBytes makes oversized lines impossible
	// in logs produced by this version; the guard below is defense-in-depth
	// (older/foreign logs) so a single entry can never balloon the finalize
	// memory unit past ~2x finalizeChunkBytes.
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		line, readErr := reader.ReadBytes('\n')
		line = bytes.TrimSuffix(line, []byte("\n"))
		if len(line) > maxRecordedEventBytes {
			log.With("sid", r.sessionID).Warnf(
				"skipping oversized recorded event (%d bytes, cap %d); one replay frame lost",
				len(line), maxRecordedEventBytes)
		} else if len(line) > 0 {
			if err := appendEntry(line); err != nil {
				return entryCount, entryBytes, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return entryCount, entryBytes, fmt.Errorf("failed reading temp file: %w", readErr)
		}
	}

	return entryCount, entryBytes, flush()
}

// GetSessionID returns the session ID
func (r *RDPSessionRecorder) GetSessionID() string {
	return r.sessionID
}
