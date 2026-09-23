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
// for the identity header. Go's own server stops at 1 MiB; an authenticating
// proxy adds a handful of headers to whatever the client sent, so 64 KiB
// leaves room and still refuses a client that streams headers forever.
const maxRequestHead = 64 << 10

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

	// Buffered so the read does not fragment the stream: the gate's first
	// Read gets the whole head plus whatever followed it in the same
	// segment, exactly as it would have without the peek.
	br := bufio.NewReaderSize(conn, maxRequestHead)

	// Peek(n) blocks until n bytes are buffered, so growing n by one past
	// what is already held forces exactly one more socket read per round;
	// a head that arrives in one segment costs one round. The separator
	// search restarts three bytes before the previous end, so a head that
	// trickles in byte by byte is scanned once, not once per byte.
	head, err := br.Peek(1)
	scanned := 0
	for err == nil {
		if i := bytes.Index(head[scanned:], []byte("\r\n\r\n")); i >= 0 {
			head = head[:scanned+i+4]
			break
		}
		if len(head) >= maxRequestHead {
			return nil, "", fmt.Errorf("request head exceeds %d bytes", maxRequestHead)
		}
		scanned = max(0, len(head)-3)
		head, err = br.Peek(len(head) + 1)
	}
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, "", fmt.Errorf("client closed before sending a request: %w", err)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			return nil, "", fmt.Errorf("request head exceeds %d bytes", maxRequestHead)
		}
		return nil, "", fmt.Errorf("reading the client's first request: %w", err)
	}

	var subject string
	// head ends at the blank line, so ReadRequest sees every header and no
	// body; a body it would otherwise wait for is still in br for the gate.
	if req, perr := http.ReadRequest(bufio.NewReader(bytes.NewReader(head))); perr == nil {
		subject = req.Header.Get(header)
	}
	return finishNegotiation(conn, br, timeout, subject)
}
