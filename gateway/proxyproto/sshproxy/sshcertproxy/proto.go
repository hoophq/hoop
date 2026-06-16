package sshcertproxy

import "golang.org/x/crypto/ssh"

// ChannelHandler abstracts all protocol-specific behavior for a single SSH
// proxy connection. Each protocol handler (pgproto, sshproto) implements this
// interface and owns its own gRPC read loop and session open handshake.
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
