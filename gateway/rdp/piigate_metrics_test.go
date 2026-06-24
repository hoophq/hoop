package rdp

import (
	"testing"
	"time"
)

func TestPercentileMs_NearestRank(t *testing.T) {
	s := []int64{10000, 20000, 30000, 40000, 50000} // micros
	cases := []struct {
		q    float64
		want float64 // ms
	}{
		{0.0, 10.0},
		{0.5, 30.0},
		{1.0, 50.0},
		{0.95, 50.0}, // rank round(0.95*4)=round(3.8)=4 -> 50ms
	}
	for _, c := range cases {
		if got := percentileMs(s, c.q); got != c.want {
			t.Errorf("percentileMs(q=%.2f) = %.1f, want %.1f", c.q, got, c.want)
		}
	}
}

func TestPercentileMs_Empty(t *testing.T) {
	if got := percentileMs(nil, 0.5); got != 0 {
		t.Errorf("percentileMs(empty) = %.1f, want 0", got)
	}
}

func TestStageSamples_SummarizeEmpty(t *testing.T) {
	var s piiStageSamples
	if got := s.summarize(); got != "n=0" {
		t.Errorf("summarize(empty) = %q, want %q", got, "n=0")
	}
}

func TestStageSamples_PushIgnoresNonPositive(t *testing.T) {
	var s piiStageSamples
	s.push(0)
	s.push(-1 * time.Millisecond)
	s.push(2 * time.Millisecond)
	if len(s.micros) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(s.micros))
	}
	if s.micros[0] != 2000 {
		t.Errorf("expected 2000us, got %d", s.micros[0])
	}
}

func TestStageSamples_CappedPerWindow(t *testing.T) {
	var s piiStageSamples
	for i := 0; i < piiMaxSamplesPerStage+100; i++ {
		s.push(time.Millisecond)
	}
	if len(s.micros) != piiMaxSamplesPerStage {
		t.Errorf("samples = %d, want cap %d", len(s.micros), piiMaxSamplesPerStage)
	}
	if s.summarize() == "n=0" {
		t.Error("capped stage should still summarize")
	}
}

func TestAggregator_RecordBatchAccumulates(t *testing.T) {
	a := newPIILatencyAggregator("sid")
	a.recordBatch(time.Millisecond, 5*time.Millisecond, 3*time.Millisecond, 9*time.Millisecond, 2048, true)
	a.recordBatch(time.Millisecond, 0, 0, 2*time.Millisecond, 1024, false)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.batches != 2 {
		t.Errorf("batches = %d, want 2", a.batches)
	}
	if a.detections != 1 {
		t.Errorf("detections = %d, want 1", a.detections)
	}
	if a.forwardedBytes != 3072 {
		t.Errorf("forwardedBytes = %d, want 3072", a.forwardedBytes)
	}
	// OCR ran once (second batch had 0 -> skipped); total ran twice.
	if len(a.ocr.micros) != 1 {
		t.Errorf("ocr samples = %d, want 1", len(a.ocr.micros))
	}
	if len(a.total.micros) != 2 {
		t.Errorf("total samples = %d, want 2", len(a.total.micros))
	}
}

func TestAggregator_StartsLazilyAndFlushResets(t *testing.T) {
	a := newPIILatencyAggregator("sid")
	if !a.startedAt.IsZero() {
		t.Fatal("startedAt should be zero before first record")
	}
	a.recordBatch(0, 0, 0, time.Millisecond, 100, false)
	if a.startedAt.IsZero() {
		t.Fatal("startedAt should be set after first record")
	}
	a.flushFinal()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.batches != 0 || !a.startedAt.IsZero() {
		t.Errorf("flushFinal should reset window: batches=%d startedAtZero=%v", a.batches, a.startedAt.IsZero())
	}
}

func TestAggregator_FlushFinalNoopWhenEmpty(t *testing.T) {
	a := newPIILatencyAggregator("sid")
	// Should not panic and should leave the window empty.
	a.flushFinal()
	if a.batches != 0 {
		t.Errorf("batches = %d, want 0", a.batches)
	}
}
