package main

import (
	"fmt"
	"sort"
	"time"
)

// durationStats accumulates duration samples and reports summary statistics.
type durationStats struct {
	samples []time.Duration
}

func (s *durationStats) add(d time.Duration) { s.samples = append(s.samples, d) }

func (s *durationStats) count() int { return len(s.samples) }

func (s *durationStats) percentile(p float64) time.Duration {
	if len(s.samples) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(s.samples))
	copy(sorted, s.samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

func (s *durationStats) summary() string {
	if len(s.samples) == 0 {
		return "n=0"
	}
	var total time.Duration
	for _, d := range s.samples {
		total += d
	}
	avg := total / time.Duration(len(s.samples))
	return fmt.Sprintf("n=%d min=%s avg=%s p50=%s p95=%s max=%s total=%s",
		len(s.samples),
		s.percentile(0).Round(time.Microsecond),
		avg.Round(time.Microsecond),
		s.percentile(0.50).Round(time.Microsecond),
		s.percentile(0.95).Round(time.Microsecond),
		s.percentile(1).Round(time.Microsecond),
		total.Round(time.Millisecond))
}
