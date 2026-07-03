package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// runFetch exports a session recording (blob stream + canvas dimensions)
// from the gateway database into a self-contained fixture file.
func runFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	sessionID := fs.String("session", "", "session UUID to export (required)")
	dbURI := fs.String("db", os.Getenv("POSTGRES_DB_URI"), "postgres connection URI (default: $POSTGRES_DB_URI)")
	output := fs.String("o", "recording.json", "output fixture file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("-session is required")
	}
	if *dbURI == "" {
		return fmt.Errorf("-db or POSTGRES_DB_URI is required")
	}

	db, err := gorm.Open(postgres.Open(*dbURI), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get database handle: %w", err)
	}
	defer sqlDB.Close()

	var row struct {
		BlobStream   json.RawMessage `gorm:"column:blob_stream"`
		CanvasWidth  *int            `gorm:"column:canvas_width"`
		CanvasHeight *int            `gorm:"column:canvas_height"`
	}
	tx := db.Raw(`
		SELECT b.blob_stream,
		       (s.metrics->>'canvas_width')::int  AS canvas_width,
		       (s.metrics->>'canvas_height')::int AS canvas_height
		FROM private.sessions s
		INNER JOIN private.blobs b ON b.id = s.blob_stream_id AND b.type = 'session-stream'
		WHERE s.id = ?`, *sessionID).
		Scan(&row)
	if tx.Error != nil {
		return fmt.Errorf("failed to query session %s: %w", *sessionID, tx.Error)
	}
	if tx.RowsAffected == 0 {
		return fmt.Errorf("session %s not found or has no session-stream recording", *sessionID)
	}
	if len(row.BlobStream) == 0 {
		return fmt.Errorf("session %s has an empty recording blob", *sessionID)
	}

	var events []json.RawMessage
	if err := json.Unmarshal(row.BlobStream, &events); err != nil {
		return fmt.Errorf("session %s blob stream is not a JSON event array: %w", *sessionID, err)
	}

	fixture := Fixture{
		SessionID:    *sessionID,
		CanvasWidth:  1280,
		CanvasHeight: 720,
		Events:       events,
	}
	if row.CanvasWidth != nil && *row.CanvasWidth > 0 {
		fixture.CanvasWidth = *row.CanvasWidth
	} else {
		fmt.Fprintf(os.Stderr, "warning: session has no canvas_width metric, defaulting to %d\n", fixture.CanvasWidth)
	}
	if row.CanvasHeight != nil && *row.CanvasHeight > 0 {
		fixture.CanvasHeight = *row.CanvasHeight
	} else {
		fmt.Fprintf(os.Stderr, "warning: session has no canvas_height metric, defaulting to %d\n", fixture.CanvasHeight)
	}

	// Validate the recording is replayable before writing the fixture.
	frames, err := parseEvents(events)
	if err != nil {
		return fmt.Errorf("session %s: %w", *sessionID, err)
	}

	data, err := json.Marshal(fixture)
	if err != nil {
		return fmt.Errorf("failed to marshal fixture: %w", err)
	}
	if err := os.WriteFile(*output, data, 0o600); err != nil {
		return fmt.Errorf("failed to write fixture: %w", err)
	}

	duration := frames[len(frames)-1].Timestamp - frames[0].Timestamp
	fmt.Printf("wrote %s: session=%s canvas=%dx%d events=%d bitmaps=%d duration=%.1fs size=%d bytes\n",
		*output, *sessionID, fixture.CanvasWidth, fixture.CanvasHeight,
		len(events), len(frames), duration, len(data))
	return nil
}
