package analytics

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// The two properties an operator running the sidecar in an air-gapped
// network relies on: telemetry never blocks a caller, and a destination
// nobody can reach never surfaces as an error, a hang or a crash.

func TestUnreachableEndpointsNeverBlockOrFail(t *testing.T) {
	// A port nothing listens on: connection refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := "http://" + ln.Addr().String() + "/v1/batch"
	_ = ln.Close()

	for name, url := range map[string]string{
		"refused":     refused,
		"no-such-dns": "http://sidecar-analytics.invalid/v1/batch",
	} {
		t.Run(name, func(t *testing.T) {
			c := New(Options{Endpoint: url, WriteKey: "wk"})
			start := time.Now()
			for range batchSize * 3 {
				c.Track(EventUsage, Properties{"n": 1})
			}
			if d := time.Since(start); d > 50*time.Millisecond {
				t.Fatalf("Track took %v for %d events; it must not touch the network", d, batchSize*3)
			}
			start = time.Now()
			c.Close()
			if d := time.Since(start); d > closeDeadline+time.Second {
				t.Fatalf("Close took %v, must give up by %v", d, closeDeadline)
			}
		})
	}
}

func TestBlackholedEndpointBoundsShutdown(t *testing.T) {
	// A server that accepts and never answers is the worst network: no
	// error, no bytes. The client's own timeout is the only way out.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	c := New(Options{
		Endpoint:   "http://" + ln.Addr().String() + "/v1/batch",
		WriteKey:   "wk",
		HTTPClient: &http.Client{Timeout: 200 * time.Millisecond},
	})
	c.Track(EventStarted, nil)
	start := time.Now()
	c.Close()
	if d := time.Since(start); d > closeDeadline+time.Second {
		t.Fatalf("Close took %v against a black hole; must be bounded", d)
	}
}

// A panic in the sender must end the sender and nothing else: Track keeps
// returning, Close keeps returning, the process keeps running.
func TestSenderPanicIsContained(t *testing.T) {
	c := New(Options{Endpoint: "http://127.0.0.1:1/v1/batch", WriteKey: "wk"})
	// A transport that panics stands in for any bug below Track.
	c.http = &http.Client{Transport: panicTransport{}}
	c.Track(EventStarted, nil)
	c.Close() // flushes: the sender runs send, which panics, which is swallowed.
	c.Track(EventStopped, nil)
	c.Close()
}

type panicTransport struct{}

func (panicTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("telemetry bug")
}

// The per-statement cost the gate pays.
func BenchmarkLaneCounterStatement(b *testing.B) {
	l := NewCounters().Lane("postgres")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Statement(false, "")
		}
	})
}
