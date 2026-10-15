package gate

import (
	"context"
	"strconv"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// SettleFunc reports what the upstream answered to a statement a reserved
// approval released: accepted spends the approval, refused gives it back. It
// runs on the response path before the response reaches the client, so a
// client that resends after a refusal finds the approval back. It must bound
// its own time and must not depend on ctx staying live: the gate also calls
// it from Close.
type SettleFunc func(ctx context.Context, reviewID string, accepted bool)

// The gate pairs a reserved approval with the upstream's answer in one of two
// ways, by the path the statements arrive on.
//
// A byte-path gate serves one HTTP/1 connection (the h2 bridge gives every
// stream its own). Responses come back in request order, so settleQueue holds
// one entry per forwarded request, "" for a request that reserved nothing,
// and each final response takes the oldest.
//
// A statement gate lives for one gRPC call. settleOpen holds the reservations
// its requests made, and the first response that says how the call went
// settles them all.
//
// Both are guarded by Gate.mu: the two pumps of a byte-path gate push and pop
// from opposite directions.
//
// Known limit: the byte path forwards a chunk only after judging every
// request in it. A reserved request pipelined ahead of one that holds waits
// for that hold, and can reach the upstream after its lease ended, when the
// plane has already spent the approval. That is the outcome every approval
// had before reserving existed.

// reservedReview returns the id of an allowed statement's reserved approval,
// or "".
func reservedReview(j judgment) string {
	if j.denied || j.review == nil || !j.review.Reserved {
		return ""
	}
	return j.review.ID
}

// queueExchange records one forwarded HTTP request, and reports whether it
// did: a gate with no Settle tracks nothing.
func (g *Gate) queueExchange(reviewID string) bool {
	if g.cfg.Settle == nil {
		return false
	}
	g.mu.Lock()
	g.settleQueue = append(g.settleQueue, reviewID)
	g.mu.Unlock()
	return true
}

// openReservation records a statement gate's reservation, "" for none.
func (g *Gate) openReservation(reviewID string) {
	if reviewID == "" || g.cfg.Settle == nil {
		return
	}
	g.mu.Lock()
	g.settleOpen = append(g.settleOpen, reviewID)
	g.mu.Unlock()
}

// dropQueued takes back the last n requests queued, which a denial in the
// same chunk kept from being forwarded, and gives their approvals back.
func (g *Gate) dropQueued(ctx context.Context, n int) {
	g.mu.Lock()
	n = min(n, len(g.settleQueue))
	dropped := append([]string(nil), g.settleQueue[len(g.settleQueue)-n:]...)
	g.settleQueue = g.settleQueue[:len(g.settleQueue)-n]
	g.mu.Unlock()
	g.settle(ctx, dropped, false)
}

// answerExchange settles the oldest forwarded request with the final
// response stmt.
func (g *Gate) answerExchange(ctx context.Context, stmt inspect.Statement) {
	g.mu.Lock()
	if len(g.settleQueue) == 0 {
		g.mu.Unlock()
		return
	}
	reviewID := g.settleQueue[0]
	g.settleQueue = g.settleQueue[1:]
	g.mu.Unlock()
	g.settle(ctx, []string{reviewID}, acceptedHTTPStatus(stmt.HTTP.StatusCode))
}

// answerStatement settles a statement gate's open reservations on the first
// response that says how the call went.
func (g *Gate) answerStatement(ctx context.Context, stmt inspect.Statement) {
	if g.cfg.Settle == nil {
		return
	}
	accepted, decided := callOutcome(stmt)
	if !decided {
		return
	}
	g.mu.Lock()
	open := g.settleOpen
	g.settleOpen = nil
	g.mu.Unlock()
	g.settle(ctx, open, accepted)
}

// settleUnanswered spends every reservation the connection ended without an
// answer for. The upstream may have run the statement, so the approval is
// not given back.
func (g *Gate) settleUnanswered(ctx context.Context) {
	g.mu.Lock()
	pending := append(g.settleQueue, g.settleOpen...)
	g.settleQueue, g.settleOpen = nil, nil
	g.mu.Unlock()
	g.settle(ctx, pending, true)
}

func (g *Gate) settle(ctx context.Context, reviewIDs []string, accepted bool) {
	if g.cfg.Settle == nil {
		return
	}
	for _, id := range reviewIDs {
		if id != "" {
			g.cfg.Settle(ctx, id, accepted)
		}
	}
}

// acceptedHTTPStatus: a 4xx means the upstream refused the request, so per
// RFC 9110 it applied nothing. Every other final status may have run it,
// including a 5xx: a 500 or a 504 can follow work the upstream already did.
func acceptedHTTPStatus(code int) bool {
	return code < 400 || code >= 500
}

// callOutcome reads how a gRPC call went from a response statement. A
// response message means the call is running. The trailer statement carries
// the grpc-status; any other server statement decides nothing.
func callOutcome(stmt inspect.Statement) (accepted, decided bool) {
	if stmt.Direction != inspect.FromServer {
		return false, false
	}
	raw, ok := stmt.Metadata[inspect.MetadataGRPCStatusCode]
	if !ok {
		return true, true
	}
	code, err := strconv.Atoi(raw)
	if err != nil {
		return true, true
	}
	return !refusedGRPCCode(code), true
}

// refusedGRPCCode reports a status by which the server refused the call
// before running it: the codes the canonical HTTP mapping puts in 4xx, but
// CANCELLED, which the client sends and which can follow work already done,
// plus UNIMPLEMENTED, which names a method the server does not have.
func refusedGRPCCode(code int) bool {
	switch code {
	case 3, // INVALID_ARGUMENT
		5,  // NOT_FOUND
		6,  // ALREADY_EXISTS
		7,  // PERMISSION_DENIED
		8,  // RESOURCE_EXHAUSTED
		9,  // FAILED_PRECONDITION
		10, // ABORTED
		11, // OUT_OF_RANGE
		12, // UNIMPLEMENTED
		16: // UNAUTHENTICATED
		return true
	}
	return false
}
