package rdp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/rdp/parser"
)

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
		parser:        GetParser(),
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

	if r.closed || len(data) == 0 {
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

		// Write to temp file (one JSON line per event)
		if _, err := r.tmpFile.Write(line); err != nil {
			log.With("sid", r.sessionID).Errorf("failed to write bitmap to temp file: %v", err)
			return
		}
		if _, err := r.tmpFile.Write([]byte("\n")); err != nil {
			log.With("sid", r.sessionID).Errorf("failed to write newline to temp file: %v", err)
			return
		}

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

	if r.tmpFile == nil {
		return
	}

	// Close temp file for writing, then read it back
	tmpPath := r.tmpFile.Name()
	r.tmpFile.Close()
	defer os.Remove(tmpPath)

	// Build the final JSON array by reading back from the temp file
	// Format: [ handshake_event, bitmap_event_1, bitmap_event_2, ... ]
	eventsJSON, readErr := r.buildEventsJSON(tmpPath)
	if readErr != nil {
		log.With("sid", r.sessionID).Errorf("failed to build events JSON: %v", readErr)
		return
	}

	eventSize := int64(len(eventsJSON))

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
		BlobStream: json.RawMessage(eventsJSON),
		Status:     string(openapi.SessionStatusDone),
		ExitCode:   exitCode,
		EndSession: &endTime,
	}

	if dbErr := models.UpdateSessionEventStream(sessDone); dbErr != nil {
		log.With("sid", r.sessionID).Errorf("failed to finalize RDP session: %v", dbErr)
		return
	}

	// Enqueue async PII analysis if there are bitmap frames to analyze, the
	// analysis pipeline is actually enabled on this gateway, AND the org has
	// the experimental flag turned on. Otherwise the job would sit 'pending'
	// forever (worker pool may be stopped by the supervisor) and the session
	// would appear stuck.
	if r.bitmapCount > 0 &&
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

// buildEventsJSON reads bitmap events from the temp file and builds the final JSON array
func (r *RDPSessionRecorder) buildEventsJSON(tmpPath string) ([]byte, error) {
	tmpData, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read temp file: %w", err)
	}

	// Build JSON array: [handshake, event1, event2, ...]
	// Start with "["
	var result []byte
	result = append(result, '[')

	// Add handshake event first if available
	first := true
	if len(r.handshakeData) > 0 {
		handshakeEvent := [3]any{
			0,
			"h",
			base64.StdEncoding.EncodeToString(r.handshakeData),
		}
		hJSON, _ := json.Marshal(handshakeEvent)
		result = append(result, hJSON...)
		first = false
	}

	// Append each line from the temp file (each line is a JSON event)
	start := 0
	for i := 0; i < len(tmpData); i++ {
		if tmpData[i] == '\n' {
			line := tmpData[start:i]
			if len(line) > 0 {
				if !first {
					result = append(result, ',')
				}
				result = append(result, line...)
				first = false
			}
			start = i + 1
		}
	}

	result = append(result, ']')
	return result, nil
}

// GetSessionID returns the session ID
func (r *RDPSessionRecorder) GetSessionID() string {
	return r.sessionID
}
