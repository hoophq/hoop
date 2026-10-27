package gate

import (
	"fmt"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// MaskScope says how a gate ties a mask an evaluator applies to one statement
// (policy.EvalContext.MaskResponse) to the response of that statement, and to
// no other. It is a property of the protocol, which only the caller knows.
//
// The lane's own Masker is never changed: it is shared by every connection.
// A plan lives on the gate of one connection, or of one RPC.
//
// Where a response cannot be tied to its statement, a statement that carries
// a plan is denied with a message that says why. Masking the wrong response
// and skipping the mask are both worse than a refusal.
type MaskScope uint8

const (
	// MaskScopeNone ties nothing: a statement that carries a plan is
	// denied. It is the zero value, so a caller that sets nothing fails
	// closed.
	MaskScopeNone MaskScope = iota

	// MaskScopeExchange is a gate that serves one exchange: a
	// NewStatementGate per gRPC call. A plan applies to every response
	// the gate masks after its statement, through MaskCell.
	MaskScopeExchange

	// MaskScopeSerial is a byte-path database lane whose client waits for
	// a response before it sends the next statement. A plan applies to
	// every server byte from its statement until the next client
	// statement, and that statement is refused until the protocol's own
	// end-of-response marker has arrived (ResponseEnd): postgres
	// ReadyForQuery, the TDS packet that ends a message. A protocol with
	// no marker the gate reads keeps the plan, and refuses every later
	// statement, for the life of the connection. A plan on a statement
	// that arrived with others in one read is refused too.
	MaskScopeSerial

	// MaskScopeHTTP is a byte-path HTTP/1 lane: responses come in request
	// order, and the codec reports each final response. A plan applies to
	// the response of its request, and ends with that final response. A
	// plan on a request that another outstanding request precedes, a
	// request sent while a plan is active, and a plan on a WebSocket
	// message are refused.
	MaskScopeHTTP
)

// SourceMaskScope is a statement refused because the gate could not tie a
// response mask to it.
const SourceMaskScope = "mask_scope"

// maskScopeRule is the rule name of a mask scope refusal.
const maskScopeRule = "ai-mask"

// bytePath reports a scope a byte-path gate serves.
func (s MaskScope) bytePath() bool { return s == MaskScopeSerial || s == MaskScopeHTTP }

// isRequest reports a client statement that opens an exchange under the
// gate's scope.
func (g *Gate) isRequest(stmt inspect.Statement) bool {
	if stmt.Direction != inspect.FromClient {
		return false
	}
	if g.maskScope == MaskScopeHTTP {
		return isHTTPRequest(stmt)
	}
	return true
}

// countRequests counts the statements of one client read that open an
// exchange.
func (g *Gate) countRequests(stmts []inspect.Statement) int {
	n := 0
	for _, s := range stmts {
		if g.isRequest(s) {
			n++
		}
	}
	return n
}

// exchangeBusy refuses a client statement sent while a plan is active and
// its response cannot be told apart from this statement's. On a serial lane
// a statement after the masked response ended ends the plan.
func (g *Gate) exchangeBusy(stmt inspect.Statement) string {
	if !g.maskScope.bytePath() || stmt.Direction != inspect.FromClient {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.planActive {
		return ""
	}
	switch g.maskScope {
	case MaskScopeHTTP:
		return "an AI data masking rule applies to the response of the previous request on this " +
			"connection, which has not arrived yet; send this request after that response"
	case MaskScopeSerial:
		if g.inFlight {
			if g.endScan == nil {
				return fmt.Sprintf("an AI data masking rule applies to the response of an earlier statement "+
					"on this connection, and a %s lane cannot see where that response ends, so the "+
					"connection takes no further statement; reconnect", g.cfg.Protocol)
			}
			return "an AI data masking rule applies to the response of the previous statement on this " +
				"connection, which has not ended yet; send this statement after that response"
		}
		g.retirePlan()
	}
	return ""
}

// maskRefusal says why the plan a statement carries cannot be tied to its
// response, or "". requests counts the statements of the read it arrived in.
func (g *Gate) maskRefusal(stmt inspect.Statement, requests int) string {
	const head = "an AI data masking rule applies to the response of this statement, and "
	switch g.maskScope {
	case MaskScopeExchange:
		return ""
	case MaskScopeNone:
		return fmt.Sprintf(head+"a %s lane cannot tie a response to one statement, so it is refused",
			g.cfg.Protocol)
	}
	if !g.isRequest(stmt) {
		return head + "this message has no response of its own, so it is refused"
	}
	if requests > 1 {
		return head + "it arrived with other statements in one read, so its response cannot be told " +
			"apart from theirs; send it alone"
	}
	if g.maskScope == MaskScopeHTTP {
		g.mu.Lock()
		open := g.httpOpen
		g.mu.Unlock()
		if open > 0 {
			return head + "an earlier request on this connection is still waiting for its response; " +
				"send this request after that response"
		}
	}
	return ""
}

// installPlan makes masks the plan of the statement just allowed. Called
// once the statement will be forwarded.
func (g *Gate) installPlan(masks []policy.ResponseMask) {
	if len(masks) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.maskScope == MaskScopeExchange {
		// One exchange: every request message of the call adds to it.
		g.plan = append(g.plan, masks...)
		g.planActive = true
		return
	}
	g.plan = masks
	g.planActive = true
	g.inFlight = true
}

// retirePlan ends the active plan. Called with mu held.
func (g *Gate) retirePlan() {
	g.plan, g.planActive, g.inFlight = nil, false, false
}

// serverRead snapshots the plan for one server read, and on a serial lane
// reports whether the read ends a response. The snapshot masks the whole
// read, so a statement the client sends meanwhile cannot change the mask
// halfway through it. The end scanner reads every server byte, plan or
// not, because it must stay on the protocol's framing.
func (g *Gate) serverRead(data []byte) (plan []policy.ResponseMask, ended bool) {
	if !g.maskScope.bytePath() || len(data) == 0 {
		return nil, false
	}
	if g.endScan != nil {
		ended = g.endScan.Ended(data)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.planActive {
		return nil, ended
	}
	return g.plan, ended
}

// answered records the final responses of one server read on an HTTP lane.
// The plan ends with the final response of its request, after the read that
// carried it was masked.
func (g *Gate) answered(stmts []inspect.Statement, ended bool) {
	if g.maskScope == MaskScopeSerial {
		if ended {
			g.mu.Lock()
			g.inFlight = false
			g.mu.Unlock()
		}
		return
	}
	if g.maskScope != MaskScopeHTTP {
		return
	}
	finals := 0
	for _, s := range stmts {
		if isFinalHTTPResponse(s) {
			finals++
		}
	}
	if finals == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.httpOpen = max(g.httpOpen-finals, 0)
	if g.planActive && g.httpOpen == 0 {
		g.retirePlan()
	}
}

// sent records one client read that was forwarded. On an HTTP lane it counts
// the requests; on a serial lane any client byte sent while a plan is active
// opens a response the plan must cover, statement or not (a pgwire Bind and
// Execute of a statement parsed earlier carries none).
func (g *Gate) sent(stmts []inspect.Statement) {
	switch g.maskScope {
	case MaskScopeSerial:
		g.mu.Lock()
		if g.planActive {
			g.inFlight = true
		}
		g.mu.Unlock()
	case MaskScopeHTTP:
		n := g.countRequests(stmts)
		if n == 0 {
			return
		}
		g.mu.Lock()
		g.httpOpen += n
		g.mu.Unlock()
	}
}

// activePlan is the plan a flush or a statement-path mask applies now.
func (g *Gate) activePlan() []policy.ResponseMask {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.planActive {
		return nil
	}
	return g.plan
}

// cellMasker applies the lane's masker, then every mask of plan, to one
// value, and reports the entities and how many values changed.
func (g *Gate) cellMasker(plan []policy.ResponseMask) func(column string, value []byte) ([]byte, []string, int) {
	return func(column string, value []byte) ([]byte, []string, int) {
		var entities []string
		count := 0
		if g.masker != nil {
			out, names, n := g.masker.MaskCell(column, value)
			if n > 0 {
				value, entities, count = out, append(entities, names...), count+n
			}
		}
		for _, m := range plan {
			out, names, n := m.MaskCell(column, value)
			if n > 0 {
				value, entities, count = out, append(entities, names...), count+n
			}
		}
		return value, entities, count
	}
}

// MaskCell masks one value of a response this gate's caller decodes itself,
// the gRPC lane: the lane's masker, then the plan of this exchange.
func (g *Gate) MaskCell(column string, value []byte) ([]byte, []string, int) {
	return g.cellMasker(g.activePlan())(column, value)
}

// masksResponses reports a gate whose server stream goes through the
// reframer: a lane masker, or a scope that may hold a plan.
func (g *Gate) masksResponses() bool {
	return g.reframer != nil && (g.masker != nil || g.maskScope.bytePath())
}
