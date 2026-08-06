package memory

import (
	"reflect"
	"testing"
)

func TestRingAppendBelowCapacity(t *testing.T) {
	r := NewRing[int](5)
	if got := r.Snapshot(); got != nil {
		t.Fatalf("empty ring snapshot = %v, want nil", got)
	}
	if got := r.LatestSeq(); got != 0 {
		t.Fatalf("empty ring LatestSeq = %d, want 0", got)
	}
	for i := 1; i <= 3; i++ {
		r.Append(i)
	}
	if got, want := r.Snapshot(), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	if got := r.LatestSeq(); got != 3 {
		t.Fatalf("LatestSeq = %d, want 3", got)
	}
}

func TestRingWraparoundEviction(t *testing.T) {
	r := NewRing[int](3)
	for i := 1; i <= 5; i++ {
		r.Append(i)
	}
	if got, want := r.Snapshot(), []int{3, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot after wrap = %v, want %v", got, want)
	}
	if got := r.LatestSeq(); got != 5 {
		t.Fatalf("LatestSeq = %d, want 5", got)
	}
}

func TestRingSince(t *testing.T) {
	r := NewRing[int](3)
	for i := 1; i <= 5; i++ {
		r.Append(i) // seqs 1..5; buffer holds seqs 3,4,5
	}
	// mid-window
	got, seq := r.Since(4)
	if want := []int{5}; !reflect.DeepEqual(got, want) || seq != 5 {
		t.Fatalf("Since(4) = %v, %d; want %v, 5", got, seq, want)
	}
	// pre-window (older than evicted entries): returns everything buffered
	got, seq = r.Since(1)
	if want := []int{3, 4, 5}; !reflect.DeepEqual(got, want) || seq != 5 {
		t.Fatalf("Since(1) = %v, %d; want %v, 5", got, seq, want)
	}
	// at latest: nothing newer, cursor unchanged
	got, seq = r.Since(5)
	if got != nil || seq != 5 {
		t.Fatalf("Since(5) = %v, %d; want nil, 5", got, seq)
	}
	// empty ring
	empty := NewRing[int](2)
	got, seq = empty.Since(0)
	if got != nil || seq != 0 {
		t.Fatalf("empty Since(0) = %v, %d; want nil, 0", got, seq)
	}
}

func TestRingLatestSeqMonotonic(t *testing.T) {
	r := NewRing[int](2)
	var prev uint64
	for i := range 10 {
		r.Append(i)
		cur := r.LatestSeq()
		if cur <= prev {
			t.Fatalf("LatestSeq not monotonic: prev=%d cur=%d", prev, cur)
		}
		prev = cur
	}
}

func TestRingPanicsOnBadCapacity(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewRing(0) did not panic")
		}
	}()
	NewRing[int](0)
}
