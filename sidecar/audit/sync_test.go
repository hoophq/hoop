package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A trail written to a pipe (the default audit.file is stdout, and a
// container or `| tee` makes that a pipe) closes without an error: fsync on a
// pipe fails although every byte was delivered, and that error at every
// shutdown would make an operator doubt a complete trail.
func TestClosingASinkOnAPipeReportsNothing(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go func() { _, _ = drain(r) }()

	s := NewJSONLSink(w, SinkOptions{})
	if err := s.Write(context.Background(), Event{Kind: KindStatement, SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing a sink on a pipe failed: %v", err)
	}
	_ = w.Close()
}

// A regular file is still synced, and its trail is on disk at Close.
func TestClosingASinkOnAFileSyncsIt(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !syncable(f) {
		t.Fatal("a regular file is not synced")
	}
	s := NewJSONLSink(f, SinkOptions{})
	if err := s.Write(context.Background(), Event{Kind: KindStatement, SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing a sink on a file failed: %v", err)
	}
	if fi, _ := f.Stat(); fi.Size() == 0 {
		t.Fatal("the trail is not on disk")
	}
}

func drain(f *os.File) (int64, error) {
	buf := make([]byte, 4096)
	var n int64
	for {
		m, err := f.Read(buf)
		n += int64(m)
		if err != nil {
			return n, err
		}
	}
}
