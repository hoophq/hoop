package sshcertproxy

import "golang.org/x/crypto/ssh"

// ChannelHandler abstracts all protocol-specific behavior for a single SSH
// proxy connection. Each protocol handler (pgproto, sshproto, termproto)
// implements this interface and owns its own gRPC read loop and session open
// handshake.
type ChannelHandler interface {
	// AcceptAndServe accepts the incoming SSH channel and starts serving it.
	AcceptAndServe(newCh ssh.NewChannel, channelID uint16) error
	// RangeChannels iterates over open channels for error broadcast.
	RangeChannels(fn func(key, value any) bool)
	// Wait blocks until all channel goroutines finish.
	Wait()
	// SendClose sends the SessionClose packet to the agent.
	SendClose() error
	// Close shuts down the underlying gRPC transport.
	Close() error
}

// SessionHandler extends ChannelHandler for protocols that support SSH session
// channels (pty/exec). Both sshproto.Handler and termproto.Handler satisfy
// this interface.
type SessionHandler interface {
	ChannelHandler
	// ServeSession serves an already-accepted session channel. preRequests are
	// pty-req requests pre-approved by the caller; execRequest is the client's
	// exec request used to send the final reply; upstreamCommand is the command
	// to execute on the target (empty for interactive PTY sessions).
	ServeSession(
		clientCh ssh.Channel,
		clientRequests <-chan *ssh.Request,
		channelID uint16,
		preRequests []*ssh.Request,
		execRequest *ssh.Request,
		upstreamCommand string,
	) error
}
