package proxy

import (
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// writeBeforeClose reports a write that did not finish, so the relay can
// skip the deny frame that would land inside what was cut short.
func TestWriteBeforeCloseReportsAFailedWrite(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if !writeBeforeClose(nil, nil, "nothing", inspect.FromServer, log) {
		t.Error("nothing to write was reported as a failure")
	}

	client, server := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, client) }()
	if !writeBeforeClose(server, []byte("HTTP/1.1 200 OK\r\n\r\n"), "response", inspect.FromServer, log) {
		t.Error("a write that went out was reported as a failure")
	}

	client.Close()
	if writeBeforeClose(server, []byte("HTTP/1.1 200 OK\r\n\r\n"), "response", inspect.FromServer, log) {
		t.Error("a write to a closed connection was reported as delivered")
	}
}
