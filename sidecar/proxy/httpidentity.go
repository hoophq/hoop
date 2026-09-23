package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// maxRequestHead bounds how much of the first request is read while looking
// for the identity header. It is the HTTP codec's own head limit
// (libhoop/v2/codec/http, maxHeaderBytes, 1 MiB; the same as net/http's
// DefaultMaxHeaderBytes), so turning identity_header on refuses no request
// the lane would have refused anyway. The codec does not export the
// constant; TestHTTPIdentityPeekAcceptsWhatTheCodecAccepts pins the peek to
// it. The codec applies its limit to a head it is still assembling, so a
// head that lands whole in one read past 1 MiB slips through it and not
// through this; that edge is accepted rather than matched.
const maxRequestHead = 1 << 20

// initialHeadBuffer is where the look-ahead starts. Most heads fit in one
// segment; the buffer doubles up to maxRequestHead only for the ones that
// do not, so the common connection never pays for the limit.
const initialHeadBuffer = 4 << 10

// peekHTTPIdentity reads the head of the first HTTP/1 request on conn
// WITHOUT consuming it and returns the value of header, or "" when the
// request does not carry one.
//
// # Why the relay reads ahead of the gate
//
// The gate snapshots the session's identity when it is built: the policy
// context every rule sees and the session_start audit row are both written
// before the first request byte is inspected. An identity learned from the
// request AFTER that point would leave the trail saying "anonymous" while
// the statements say otherwise. Reading the head here, before the session
// exists, is the same ordering pgwire gets from negotiateDownstream, where
// the StartupMessage names the user ahead of the gate.
//
// HTTP has no server greeting: the client always speaks first, so blocking
// on its first bytes is the protocol's own ordering. The deadline bounds a
// client that connects and never sends; it closes with no session, the same
// as a pgwire client that abandons its handshake.
//
// # What the value is worth
//
// Everything. The header is trusted exactly as far as the network is: an
// authenticating proxy that is the only thing able to reach this listener
// set it from a verified credential, and nothing here can tell that from a
// caller who typed it. The lane documents that condition; this function
// does not check it, because it cannot.
//
// The first request names the connection. A keep-alive connection that
// carries a second request with a different header keeps the first
// identity, the same way an mTLS peer does. Per-request identity would
// mean a mutable session under both pump goroutines, and no proxy this
// lane is designed for multiplexes users on one TCP connection.
//
// A head that is not HTTP/1 is handed through unread with no identity. The
// codec refuses it with the protocol's own error, which tells the user more
// than a closed socket would.
func peekHTTPIdentity(
	conn net.Conn, header string, timeout time.Duration,
) (net.Conn, string, error) {
	if timeout > 0 {
		if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, "", err
		}
	}

	// The head is read into a buffer that grows with it rather than into a
	// bufio.Reader sized for the limit: Peek can only look as far as its
	// buffer, and a 1 MiB buffer per accepted connection would be the price
	// of a limit almost no head reaches. Everything read, head and whatever
	// followed it in the same segment, is handed back in front of the
	// connection, so the gate's first Read sees exactly the bytes it would
	// have seen without the peek.
	buf := make([]byte, 0, initialHeadBuffer)
	head := -1 // end of the head, once found
	scanned := 0
	for head < 0 {
		if len(buf) == cap(buf) {
			if len(buf) >= maxRequestHead {
				return nil, "", fmt.Errorf("request head exceeds %d bytes", maxRequestHead)
			}
			grown := make([]byte, len(buf), min(2*cap(buf), maxRequestHead))
			copy(grown, buf)
			buf = grown
		}
		n, err := conn.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		// The separator search restarts three bytes before the previous
		// end, so a head that trickles in byte by byte is scanned once.
		if i := bytes.Index(buf[scanned:], []byte("\r\n\r\n")); i >= 0 {
			head = scanned + i + 4
			break
		}
		scanned = max(0, len(buf)-3)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, "", fmt.Errorf("client closed before sending a request: %w", err)
			}
			return nil, "", fmt.Errorf("reading the client's first request: %w", err)
		}
	}

	var subject string
	// The slice ends at the blank line, so ReadRequest sees every header
	// and no body; a body it would otherwise wait for is still in buf.
	if req, perr := http.ReadRequest(bufio.NewReader(bytes.NewReader(buf[:head]))); perr == nil {
		subject = req.Header.Get(header)
	}
	if timeout > 0 {
		// The relay's own IdleTimeout governs from here.
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return nil, "", err
		}
	}
	return &prefixConn{Conn: conn, prefix: buf}, subject, nil
}

// prefixConn replays bytes the peek already read before reading from the
// connection again. It keeps the connection's writes, deadlines and
// addresses, like bufferedConn; it differs in owning a plain slice, because
// the peek reads directly rather than through a bufio.Reader.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixConn) Read(b []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(b, c.prefix)
		c.prefix = c.prefix[n:]
		if len(c.prefix) == 0 {
			c.prefix = nil // release the head once replayed
		}
		return n, nil
	}
	return c.Conn.Read(b)
}
