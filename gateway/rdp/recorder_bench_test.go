package rdp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// writeSyntheticEventFile writes a jsonl temp file shaped exactly like the one
// parseAndStorePDU produces: one `[timestamp, "b", base64(bitmapEventJSON)]`
// line per bitmap event, totalling ~totalBytes.
func writeSyntheticEventFile(tb testing.TB, totalBytes int) string {
	tb.Helper()
	tmpPath := filepath.Join(tb.TempDir(), "rdp-session-bench.jsonl")
	f, err := os.Create(tmpPath)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()

	// ~48 KiB of bitmap payload per event line, matching real band updates.
	payload := make([]byte, 48*1024)
	rnd := rand.New(rand.NewSource(1))
	rnd.Read(payload)
	b64 := base64.StdEncoding.EncodeToString(payload)

	written := 0
	ts := 0.0
	for written < totalBytes {
		line, err := json.Marshal([3]any{ts, "b", b64})
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			tb.Fatal(err)
		}
		written += len(line) + 1
		ts += 0.2
	}
	return tmpPath
}

// recorderHeapPeak samples HeapAlloc while fn runs and returns the peak delta
// over the baseline taken right before fn.
func recorderHeapPeak(fn func()) uint64 {
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	stop := make(chan struct{})
	done := make(chan struct{})
	var peak uint64
	go func() {
		defer close(done)
		var m runtime.MemStats
		ticker := time.NewTicker(500 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
			}
		}
	}()

	fn()

	close(stop)
	<-done
	if peak <= base.HeapAlloc {
		return 0
	}
	return peak - base.HeapAlloc
}

// BenchmarkRecorderFinalize measures the memory profile of turning the
// recorder's on-disk event log into the session blob at Close time. This is
// the path that ran the gateway out of memory after long high-throughput RDP
// sessions: the whole temp file is materialized (and re-copied) in heap.
//
// Workload: 64 MiB temp file (~1360 bitmap events of ~48 KiB payloads).
func BenchmarkRecorderFinalize(b *testing.B) {
	const fileBytes = 64 << 20

	tmpPath := writeSyntheticEventFile(b, fileBytes)
	r := &RDPSessionRecorder{
		sessionID:     "bench-session",
		handshakeData: []byte("bench-handshake-pdu"),
		startTime:     time.Now().UTC(),
	}

	var peakMax uint64
	var outBytes int64
	b.ReportAllocs()
	for b.Loop() {
		peak := recorderHeapPeak(func() {
			// sink mirrors the production path's cost model minus the DB:
			// each chunk is fully materialized before being handed off.
			_, entryBytes, err := r.streamEvents(tmpPath, func(chunk json.RawMessage) error {
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
			outBytes = entryBytes
		})
		if peak > peakMax {
			peakMax = peak
		}
	}

	b.SetBytes(int64(fileBytes))
	b.ReportMetric(float64(peakMax)/(1<<20), "peak_MB")
	if outBytes == 0 {
		b.Fatal(fmt.Errorf("no blob produced"))
	}
}
