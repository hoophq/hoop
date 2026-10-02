package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// ReservedPathPrefix is the path space an http lane answers itself and never
// forwards. RFC 8615 keeps /.well-known/ for site-wide metadata so it cannot
// collide with an application's own routes, and "hoop" under it is ours. The
// whole prefix is reserved, not only the routes served today, so a route
// added later cannot start shadowing an upstream that had begun to use it.
const ReservedPathPrefix = "/.well-known/hoop/"

// ReviewStatusPath answers one review's status by id, under the reserved
// prefix: GET /.well-known/hoop/reviews/<id>.
const ReviewStatusPath = ReservedPathPrefix + "reviews/"

// What a client does next about a review. Every status answer carries one,
// so a client never has to interpret a status on its own.
const (
	ReviewNextWait   = "wait"
	ReviewNextResend = "resend_identical_statement"
	ReviewNextStop   = "stop"
)

// ReviewNext maps the plane's status to what a client does next. Any status
// it does not know stops the client, because only a human can say more.
func ReviewNext(status string) string {
	switch status {
	case "PENDING":
		return ReviewNextWait
	case "APPROVED":
		return ReviewNextResend
	}
	return ReviewNextStop
}

// laneReview is the reserved path's answer: the review_status fields and
// nothing from the statement, so the data port serves nothing the denial did
// not already tell the caller.
type laneReview struct {
	ReviewStatus
	Next string `json:"next"`
}

// reviewStatusTimeout bounds the plane read behind one status request.
const reviewStatusTimeout = 10 * time.Second

// reviewStatusAnswer builds an http lane's proxy.Config.Answer. It is nil on
// every other protocol: only http has a path to reserve.
//
// It answers about reviews filed on this lane only. The plane scopes a read to
// the sidecar, so another lane's review reads as not found here, as another
// sidecar's does on the plane.
//
// reviews is nil in a process with no control plane. The prefix stays
// reserved there, answering 503, so whether a path reaches the upstream never
// depends on how the sidecar was configured.
func reviewStatusAnswer(lc ListenerConfig, listener string, reviews ReviewStatusReader, log *slog.Logger) func(context.Context, inspect.Statement) []byte {
	if inspect.Protocol(lc.Protocol) != inspect.HTTP {
		return nil
	}
	return func(ctx context.Context, stmt inspect.Statement) []byte {
		if stmt.HTTP == nil || !strings.HasPrefix(stmt.HTTP.Path, ReservedPathPrefix) {
			return nil
		}
		head := stmt.HTTP.Method == http.MethodHead
		if stmt.HTTP.Method != http.MethodGet && !head {
			return laneReply(http.StatusMethodNotAllowed, false, http.Header{"Allow": {"GET, HEAD"}},
				message("only GET and HEAD are served under "+ReservedPathPrefix))
		}
		id, ok := strings.CutPrefix(stmt.HTTP.Path, ReviewStatusPath)
		if !ok || id == "" {
			return laneReply(http.StatusNotFound, head, nil, message("not found"))
		}
		if reviews == nil {
			return laneReply(http.StatusServiceUnavailable, head, nil,
				message("review status comes from the control plane, and this sidecar has none"))
		}

		ctx, cancel := context.WithTimeout(ctx, reviewStatusTimeout)
		defer cancel()
		rev, err := reviews.ReviewStatus(ctx, id)
		if err == nil && rev.ListenerName != listener {
			err = ErrReviewNotFound
		}
		switch {
		case errors.Is(err, ErrReviewNotFound):
			return laneReply(http.StatusNotFound, head, nil, message("review not found on this sidecar"))
		case errors.Is(err, ErrPlaneTooOld):
			return laneReply(http.StatusBadGateway, head, nil, message(err.Error()))
		case err != nil:
			// The cause can name the plane's address, which a data port
			// has no reason to tell its callers. The log keeps it.
			log.Warn("review status read failed", "review", id, "error", err)
			return laneReply(http.StatusBadGateway, head, nil,
				message("the control plane could not report the review"))
		}
		return laneReply(http.StatusOK, head, nil, laneReview{ReviewStatus: rev, Next: ReviewNext(rev.Status)})
	}
}

func message(m string) any { return map[string]string{"message": m} }

// laneReply renders one JSON response that closes the connection, the same
// way a deny frame does.
func laneReply(code int, head bool, h http.Header, body any) []byte {
	raw, err := json.Marshal(body)
	if err != nil {
		// A plane timestamp outside years 0-9999 fails to marshal. A panic
		// here would end the process, not the request.
		return []byte(laneReplyFailed)
	}
	raw = append(raw, '\n')
	if h == nil {
		h = http.Header{}
	}
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	resp := &http.Response{
		StatusCode:    code,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(raw)),
		ContentLength: int64(len(raw)),
		Close:         true,
	}
	if head {
		resp.Request = &http.Request{Method: http.MethodHead}
	}
	var buf bytes.Buffer
	if err := resp.Write(&buf); err != nil {
		return []byte(laneReplyFailed)
	}
	return buf.Bytes()
}

// laneReplyFailed is the answer when a reply cannot be rendered.
const laneReplyFailed = "HTTP/1.1 500 Internal Server Error\r\n" +
	"Content-Type: application/json\r\nContent-Length: 49\r\nConnection: close\r\n\r\n" +
	`{"message":"the review status could not be read"}`
