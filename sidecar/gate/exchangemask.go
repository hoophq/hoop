package gate

import (
	"bufio"
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"strings"

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
// Every path fails closed. Where a response cannot be tied to its statement,
// the statement is denied; where a masked response might leave unmasked, the
// response is denied. Masking the wrong response and skipping the mask are
// both worse than a refusal.
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

	// MaskScopeSerial is a byte-path database lane. On a protocol whose
	// framing the gate follows (ResponseEnd and requestCounter: postgres),
	// the gate counts, from the first byte of the connection,
	// the responses the client asked for and the end-of-response markers
	// the server sent. A plan is admitted only when no response is
	// outstanding and its read asks for exactly one; it then owns the
	// next marker, and ends there. Any doubt (lost framing, a read that
	// asks for no response or for several) keeps the plan for the life of the connection,
	// which then takes no further statement. A protocol with no
	// framing the gate follows works that way from the start.
	MaskScopeSerial

	// MaskScopeHTTP is a byte-path HTTP/1 lane: responses come in request
	// order, and the codec reports each final response. A plan applies to
	// the response of its request and lasts until the next request after
	// that response. A plan behind an outstanding request, a request sent
	// before the plan's response, a plan on a WebSocket message, and a
	// masked response the codec could pass through unmasked (compressed,
	// close-delimited, an upgrade, longer than AIMaskMaxResponseBytes, or
	// a head not whole in one read) are refused.
	MaskScopeHTTP
)

// AIMaskMaxResponseBytes bounds an HTTP response an AI mask covers. It equals
// the HTTP codec's masking limit (libhoop http.DefaultMaxMessageBytes, which
// the sidecar does not override; a daemon test holds the two equal): past it
// the codec forwards a held body unmasked, so the gate refuses the response
// before the byte that would cross it.
const AIMaskMaxResponseBytes = 16 << 20

// maxResponseHead bounds the response head the gate reads to check a masked
// HTTP response.
const maxResponseHead = 64 << 10

// SourceMaskScope is a statement or response refused because the gate could
// not tie a response mask to it, or could not apply it.
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

const (
	refuseNotEnded = "an AI data masking rule applies to the response of the previous statement on this " +
		"connection, which has not ended yet; send this statement after that response"
	refuseHTTPPending = "an AI data masking rule applies to the response of the previous request on this " +
		"connection, which has not arrived yet; send this request after that response"
)

func refuseSticky(p inspect.Protocol) string {
	return fmt.Sprintf("an AI data masking rule applies to the response of an earlier statement on this "+
		"connection, and this %s connection cannot see where that response ends, so it takes no "+
		"further statement; reconnect", p)
}

// exchangeBusy refuses a client statement sent while a plan is active. On an
// HTTP lane a request after the plan's response ends the plan.
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
		if !g.planAnswered {
			return refuseHTTPPending
		}
		g.retirePlan()
	case MaskScopeSerial:
		// An active serial plan has not reached its marker: the marker
		// retires it (answered).
		if g.planSticky {
			return refuseSticky(g.cfg.Protocol)
		}
		return refuseNotEnded
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
	if stmt.Protocol == inspect.Postgres && stmt.Metadata["pg.message"] == "Parse" {
		// A Parse prepares; a later Bind and Execute run it and carry no
		// statement of their own. A plan is not tied to a prepared
		// statement or a portal, so it cannot follow them.
		return head + "it is prepared with the extended query protocol, whose later execution this " +
			"lane cannot tie a mask to; send it as a simple query"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch g.maskScope {
	case MaskScopeHTTP:
		if g.httpOpen > 0 {
			return head + "an earlier request on this connection is still waiting for its response; " +
				"send this request after that response"
		}
	case MaskScopeSerial:
		if g.endScan == nil {
			return ""
		}
		if g.countLost {
			return head + "this connection can no longer tell its responses apart; reconnect"
		}
		if g.maskOutstanding > 0 {
			return head + "an earlier statement on this connection is still waiting for its response; " +
				"send this statement after that response"
		}
	}
	return ""
}

// sent records one forwarded client read and returns how many responses it
// asks for (-1 when that is unknown). On a serial lane payload is what the
// upstream receives.
func (g *Gate) sent(stmts []inspect.Statement, payload []byte) int {
	switch g.maskScope {
	case MaskScopeSerial:
		if g.reqScan == nil {
			return -1
		}
		n, ok := g.reqScan.Requests(payload)
		g.mu.Lock()
		defer g.mu.Unlock()
		if !ok {
			g.countLost = true
			if g.planActive {
				g.planSticky = true
			}
			return -1
		}
		g.maskOutstanding += n
		return n
	case MaskScopeHTTP:
		n := g.countRequests(stmts)
		if n > 0 {
			g.mu.Lock()
			g.httpOpen += n
			g.mu.Unlock()
		}
		return n
	}
	return 0
}

// installPlan makes masks the plan of the statement just allowed and
// forwarded. asked is what sent returned for its read.
func (g *Gate) installPlan(masks []policy.ResponseMask, asked int) {
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
	g.plan, g.planActive, g.planAnswered = masks, true, false
	g.respChecked, g.respBytes = false, 0
	if g.maskScope == MaskScopeSerial {
		// maskRefusal admitted the statement with nothing outstanding, so
		// its response is the next one, unless its read asked for no
		// response or for several.
		g.planDue = 1
		g.planSticky = g.endScan == nil || g.countLost || asked != 1
	}
}

// retirePlan ends the active plan. Called with mu held.
func (g *Gate) retirePlan() {
	g.plan, g.planActive, g.planAnswered, g.planSticky, g.planDue = nil, false, false, false, 0
}

// serverRead snapshots the plan for one server read and counts the
// end-of-response markers in it. The snapshot masks the whole read, so a
// marker in it cannot unmask the bytes before it. The scanner reads every
// server byte, plan or not, because it must stay on the protocol's framing.
func (g *Gate) serverRead(data []byte) (plan []policy.ResponseMask, ended int) {
	if !g.maskScope.bytePath() || len(data) == 0 {
		return nil, 0
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

// answered records one server read after it was masked. On a serial lane each
// marker ends the oldest outstanding response, and the plan's own marker
// retires it. On an HTTP lane the final response marks the plan answered; it
// retires at the next request, after the codec released the body.
func (g *Gate) answered(stmts []inspect.Statement, ended int) {
	switch g.maskScope {
	case MaskScopeSerial:
		if ended == 0 || g.endScan == nil {
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		g.maskOutstanding -= ended
		if g.maskOutstanding < 0 {
			// More ends than requests: the count is wrong from here on.
			g.maskOutstanding, g.countLost = 0, true
			if g.planActive {
				g.planSticky = true
			}
			return
		}
		if g.planActive && !g.planSticky {
			if g.planDue -= ended; g.planDue <= 0 {
				g.retirePlan()
			}
		}
	case MaskScopeHTTP:
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
			g.planAnswered = true
		}
	}
}

// headEnd is the index just past the blank line that ends a message head, or
// -1. It accepts exactly what the HTTP codec accepts (libhoop headEndIndex):
// CRLFCRLF first, else LFLF.
func headEnd(b []byte) int {
	if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
		return i + 4
	}
	if i := bytes.Index(b, []byte("\n\n")); i >= 0 {
		return i + 2
	}
	return -1
}

// responseRefusal says why a server read of a masked HTTP response must not be
// forwarded, or "". The codec forwards a body unmasked, and reports nothing,
// when it cannot undo the body's compression within its limit or the body
// outgrows that limit; and it treats the end of its buffer as the end of a
// close-delimited body. So the gate admits a masked response only when its
// head, read whole from the first read, shows a length-delimited (or chunked)
// identity-encoded body, and refuses the read that would carry the response
// past AIMaskMaxResponseBytes. A head it cannot read whole is refused.
func (g *Gate) responseRefusal(data []byte) string {
	if g.maskScope != MaskScopeHTTP || len(data) == 0 {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.planActive || g.planAnswered {
		return ""
	}
	const head = "an AI data masking rule applies to this response, and "
	g.respBytes += len(data)
	if g.respBytes > AIMaskMaxResponseBytes {
		return fmt.Sprintf(head+"it is longer than the %d bytes the sidecar can mask, so it is refused",
			AIMaskMaxResponseBytes)
	}
	buf := data
	for !g.respChecked {
		if len(buf) == 0 {
			// Only after an interim response that ended the read: no
			// byte of the real response has arrived.
			return ""
		}
		end := headEnd(buf)
		if end < 0 {
			return head + "its head did not arrive whole in one read, so it is refused"
		}
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(buf[:end])), nil)
		if err != nil {
			return head + "its head does not parse, so it is refused"
		}
		resp.Body.Close()
		buf = buf[end:]
		if resp.StatusCode/100 == 1 && resp.StatusCode != http.StatusSwitchingProtocols {
			continue // an interim response: the real one follows
		}
		g.respChecked = true
		if why := maskableHead(resp); why != "" {
			return head + why + ", so it is refused"
		}
	}
	return ""
}

// maskableHead says why the codec might pass the body of resp through
// unmasked, or "". It admits only a length-delimited, identity-encoded body
// of a media type the codec masks, or no body at all.
func maskableHead(resp *http.Response) string {
	if resp.StatusCode == http.StatusSwitchingProtocols {
		return "it switches the connection to another protocol, which a statement's mask cannot follow"
	}
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return "" // no body
	}
	for _, enc := range resp.Header.Values("Content-Encoding") {
		for _, e := range strings.Split(enc, ",") {
			if e = strings.TrimSpace(e); e != "" && !strings.EqualFold(e, "identity") {
				return fmt.Sprintf("it is compressed (%s), which the sidecar cannot always mask", e)
			}
		}
	}
	if te := resp.TransferEncoding; len(te) > 0 {
		// The codec masks a chunked body as it streams: text in units
		// cut at 64 KiB, which can split a value, and JSON value by value,
		// falling back to those text units for a body that is not JSON.
		return fmt.Sprintf("its body is sent with transfer encoding %q, which the sidecar masks in pieces "+
			"that can split a value", strings.Join(te, ", "))
	}
	switch {
	case resp.ContentLength == 0:
		return "" // no body
	case resp.ContentLength < 0:
		return "its body ends when the connection closes, so the sidecar cannot tell when it is whole"
	case resp.ContentLength > AIMaskMaxResponseBytes:
		return fmt.Sprintf("it is longer than the %d bytes the sidecar can mask", AIMaskMaxResponseBytes)
	}
	if ct := resp.Header.Get("Content-Type"); !MaskedMediaType(ct) {
		return fmt.Sprintf("its media type %q is one the sidecar does not read, so it cannot mask it", ct)
	}
	return ""
}

// MaskedMediaType reports whether the HTTP codec masks a response body of
// Content-Type ct. It mirrors the pinned codec's classifier (libhoop
// v2/codec/http classifyBody): no type is scanned as text; JSON types
// value by value; text types whole. Any other type, and a type that does
// not parse, is forwarded as it came. A test pins this to the codec.
func MaskedMediaType(ct string) bool {
	if strings.TrimSpace(ct) == "" {
		return true
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	switch {
	case mt == "application/json", mt == "text/json", mt == "application/x-ndjson",
		strings.HasSuffix(mt, "+json"),
		strings.HasPrefix(mt, "text/"),
		mt == "application/xml", mt == "application/yaml", mt == "application/x-yaml",
		mt == "application/javascript", mt == "application/x-www-form-urlencoded":
		return true
	}
	return false
}

// activePlan is the plan a statement-path mask applies now.
func (g *Gate) activePlan() []policy.ResponseMask {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.planActive {
		return nil
	}
	return g.plan
}

// flushPlan is the plan FlushResponse applies to the bytes a re-framing codec
// still holds: the plan of the exchange that owns them. Those bytes belong
// to the exchange still in flight, so its plan is the active one, and an
// exchange with no active plan is unmasked: its plan is empty. A masked
// exchange never ends while the codec still holds its bytes: postgres
// releases held rows before ReadyForQuery, and a masked HTTP response must
// be length-delimited (responseRefusal), so the codec has released its body
// when it reports the final response.
func (g *Gate) flushPlan() []policy.ResponseMask { return g.activePlan() }

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
