package mcpserver

import (
	"context"
	"time"
)

const (
	// Empirically, MCP clients (Claude Code, Cursor, Devin) tear down the
	// streamable HTTP transport when individual tool calls block longer than
	// ~60-120s. The PRD's 30-minute aspiration is unrealistic in practice; we
	// cap at 5 minutes and recommend agents re-call on timed_out=true.
	defaultWaitTimeout = 60 * time.Second
	maxWaitTimeout     = 300 * time.Second
	pollInterval       = 2 * time.Second
)

// resolveWaitTimeout clamps a caller-supplied timeout (in seconds) into
// [pollInterval, maxWaitTimeout]. 0 / negative values resolve to
// defaultWaitTimeout.
func resolveWaitTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultWaitTimeout
	}
	d := time.Duration(seconds) * time.Second
	if d > maxWaitTimeout {
		return maxWaitTimeout
	}
	if d < pollInterval {
		return pollInterval
	}
	return d
}

// waitUntil polls fn every pollInterval until fn reports done=true, the
// timeout elapses, or ctx is cancelled. It returns the last value fn
// produced, a timedOut flag (true only when the timeout — not ctx — was the
// reason it stopped), the elapsed time, and any error fn returned.
//
// fn is invoked once eagerly so an already-terminal state returns
// immediately without a poll-interval delay. On timeout, fn is invoked one
// more time so the response reflects the most recent state.
func waitUntil[T any](
	ctx context.Context,
	timeout time.Duration,
	fn func() (T, bool, error),
) (T, bool, time.Duration, error) {
	started := time.Now()

	val, done, err := fn()
	if err != nil || done {
		return val, false, time.Since(started), err
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return val, false, time.Since(started), ctx.Err()
		case <-deadline.C:
			val, done, err = fn()
			if err != nil {
				return val, false, time.Since(started), err
			}
			return val, !done, time.Since(started), nil
		case <-ticker.C:
			val, done, err = fn()
			if err != nil || done {
				return val, false, time.Since(started), err
			}
		}
	}
}
