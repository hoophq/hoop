package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialPair stands up a httptest server that upgrades to a WebSocket and
// hands the server-side Session to the test via a channel. Returns the
// client Session and a cleanup function.
func dialPair(t *testing.T, srvHandler StreamHandler) (*Session, *Session, func()) {
	t.Helper()

	srvSessCh := make(chan *Session, 1)
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		// Allow any origin so the dial doesn't trip on the default check.
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade: %v", err)
			return
		}
		s := NewServerSession(c)
		if srvHandler != nil {
			s.SetStreamHandler(srvHandler)
		}
		srvSessCh <- s
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	cli, err := Dial(context.Background(), wsURL, DialOptions{HandshakeTimeout: 2 * time.Second})
	if err != nil {
		srv.Close()
		t.Fatalf("Dial: %v", err)
	}
	var serverSess *Session
	select {
	case serverSess = <-srvSessCh:
	case <-time.After(2 * time.Second):
		srv.Close()
		t.Fatal("timeout waiting for server session")
	}

	cleanup := func() {
		_ = cli.Close()
		_ = serverSess.Close()
		srv.Close()
	}
	return cli, serverSess, cleanup
}

func TestStreamRoundTrip(t *testing.T) {
	cli, _, cleanup := dialPair(t, func(st *Stream) {
		// Echo handler.
		defer st.Close()
		_, _ = io.Copy(st, st)
	})
	defer cleanup()

	st, err := cli.OpenStream("echo")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer st.Close()

	payload := []byte("hello tunnel")
	if _, err := st.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(st, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q want %q", got, payload)
	}
}

func TestMultipleConcurrentStreams(t *testing.T) {
	cli, _, cleanup := dialPair(t, func(st *Stream) {
		defer st.Close()
		// Reply with the stream's name so the test can verify routing.
		_, _ = st.Write([]byte(st.Name()))
	})
	defer cleanup()

	names := []string{"a", "b", "c", "d", "e"}
	var wg sync.WaitGroup
	errs := make(chan error, len(names))
	for _, n := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			st, err := cli.OpenStream(name)
			if err != nil {
				errs <- err
				return
			}
			defer st.Close()
			buf := make([]byte, len(name))
			if _, err := io.ReadFull(st, buf); err != nil {
				errs <- err
				return
			}
			if string(buf) != name {
				errs <- errors.New("name mismatch")
			}
		}(n)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestPeerCloseSurfacesEOF(t *testing.T) {
	cli, _, cleanup := dialPair(t, func(st *Stream) {
		_, _ = st.Write([]byte("done"))
		_ = st.CloseWrite()
	})
	defer cleanup()

	st, err := cli.OpenStream("anything")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	buf, err := io.ReadAll(st)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(buf) != "done" {
		t.Fatalf("got %q want %q", buf, "done")
	}
}

func TestUnknownStreamGetsError(t *testing.T) {
	// Server doesn't register a handler; client opens a stream.
	cli, _, cleanup := dialPair(t, nil)
	defer cleanup()

	st, err := cli.OpenStream("ghost")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = io.ReadAll(st)
	if err == nil {
		t.Fatal("expected error from peer with no handler, got nil")
	}
}

func TestPingPong(t *testing.T) {
	cli, _, cleanup := dialPair(t, nil)
	defer cleanup()
	if err := cli.Ping([]byte("ping-payload")); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	// Best-effort check that the session stays healthy after a ping.
	time.Sleep(50 * time.Millisecond)
	if err := cli.Context().Err(); err != nil {
		t.Fatalf("session died after ping: %v", err)
	}
}

func TestCloseTerminatesReaders(t *testing.T) {
	cli, _, cleanup := dialPair(t, func(st *Stream) {
		// Hold the stream open; the test will close the client.
		_, _ = io.ReadAll(st)
	})
	defer cleanup()

	st, err := cli.OpenStream("hang")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(st)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = cli.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Read should not have succeeded after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not unblock after session Close")
	}
}
