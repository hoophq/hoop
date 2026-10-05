package analyzer

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxErrorBytes bounds how much of a failed response is read.
const maxErrorBytes = 4 << 10

// HTTPError is a provider's HTTP answer outside 2xx.
//
// It carries the status and never the body. A provider's error body can
// echo the request, and the request is the statement: logging it would put
// a value from a customer's database in the relay's logs.
type HTTPError struct {
	Provider string
	Code     int
	Status   string

	// RetryAfter is the provider's Retry-After header. HasRetryAfter
	// separates an explicit "0", which means retry now, from an absent or
	// unreadable header, which leaves the wait to the backoff.
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: provider returned %s", e.Provider, e.Status)
}

// Retryable reports whether the same request may succeed later: a rate limit
// (429), a request timeout (408), or a server-side failure (500, 502, 503,
// 504). Every other status describes the request or the credential, and a
// retry would only spend the deadline to get the same answer.
func (e *HTTPError) Retryable() bool {
	switch e.Code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// ResponseError describes a non-2xx response. It drains a bounded amount of
// the body so the connection can be reused, and discards it.
func ResponseError(provider string, resp *http.Response) error {
	_, _ = io.CopyN(io.Discard, resp.Body, maxErrorBytes)
	wait, ok := retryAfter(resp.Header.Get("Retry-After"), time.Now())
	return &HTTPError{
		Provider:      provider,
		Code:          resp.StatusCode,
		Status:        resp.Status,
		RetryAfter:    wait,
		HasRetryAfter: ok,
	}
}

// retryAfter reads a Retry-After value, either delay-seconds or an HTTP date.
// ok is false when the header is absent or unreadable. A date already past
// is a valid answer: retry now.
func retryAfter(v string, now time.Time) (wait time.Duration, ok bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if at, err := http.ParseTime(v); err == nil {
		if at.After(now) {
			return at.Sub(now), true
		}
		return 0, true
	}
	return 0, false
}
