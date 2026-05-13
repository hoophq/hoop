package sshproxy

import (
	"bytes"
	"errors"
	"io"

	"golang.org/x/crypto/ssh"
)

// stderrStubChannel implements just enough of ssh.Channel for the
// usermessage_test.go suite. Methods unused by notifyOpenChannels
// return zero values / nil. Stderr() returns a *stderrStubWriter so
// the tests can inspect what was written.
//
// The real ssh.Channel interface has more methods than we need; the
// blank implementations exist purely so the type assertion in
// notifyOpenChannels succeeds. CloseCount lets tests verify the
// channel was closed after notify, which matters because notify is
// what unblocks handleChannel's read goroutine on the gateway shutdown
// path.
type stderrStubChannel struct {
	Buffer     bytes.Buffer
	failWrite  bool
	CloseCount int
}

func (s *stderrStubChannel) Read(p []byte) (int, error)  { return 0, io.EOF }
func (s *stderrStubChannel) Write(p []byte) (int, error) { return len(p), nil }
func (s *stderrStubChannel) Close() error {
	s.CloseCount++
	return nil
}
func (s *stderrStubChannel) CloseWrite() error { return nil }
func (s *stderrStubChannel) SendRequest(string, bool, []byte) (bool, error) {
	return false, nil
}
func (s *stderrStubChannel) Stderr() io.ReadWriter {
	return (*stderrStubWriter)(s)
}

// Compile-time assertion that we satisfy ssh.Channel.
var _ ssh.Channel = (*stderrStubChannel)(nil)

// stderrStubWriter is the io.ReadWriter exposed via Stderr(). It
// shares state with its parent stderrStubChannel via unsafe pointer
// aliasing of the same struct — the field set used here (Buffer,
// failWrite) is identical on both views.
type stderrStubWriter stderrStubChannel

func (w *stderrStubWriter) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (w *stderrStubWriter) Write(p []byte) (int, error) {
	if w.failWrite {
		return 0, errors.New("stub stderr: simulated failure")
	}
	return w.Buffer.Write(p)
}
