package upgrade

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchLatestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "1.74.5")
	}))
	t.Cleanup(srv.Close)

	got, err := fetchLatestVersionFrom(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.74.5" {
		t.Fatalf("got %q, want %q", got, "1.74.5")
	}
}

func TestFetchLatestVersionStripsLeadingV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "v1.74.5")
	}))
	t.Cleanup(srv.Close)

	got, err := fetchLatestVersionFrom(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.74.5" {
		t.Fatalf("got %q, want %q", got, "1.74.5")
	}
}

func TestFetchLatestVersionEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	_, err := fetchLatestVersionFrom(srv.URL, srv.Client())
	if err == nil {
		t.Fatalf("expected error for empty body")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty body: %v", err)
	}
}

func TestFetchLatestVersionNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	t.Cleanup(srv.Close)

	_, err := fetchLatestVersionFrom(srv.URL, srv.Client())
	if err == nil {
		t.Fatalf("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention the status: %v", err)
	}
}

func TestFetchLatestVersionRejectsBelowFloor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "1.50.0")
	}))
	t.Cleanup(srv.Close)

	_, err := fetchLatestVersionFrom(srv.URL, srv.Client())
	if err == nil {
		t.Fatalf("expected error for below-floor version")
	}
	if !errors.Is(err, ErrBelowFloor) {
		t.Errorf("expected ErrBelowFloor sentinel, got: %v", err)
	}
}

func TestFetchLatestVersionRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strings.Repeat("a", latestResponseLimit+10)))
	}))
	t.Cleanup(srv.Close)

	_, err := fetchLatestVersionFrom(srv.URL, srv.Client())
	if err == nil {
		t.Fatalf("expected error for oversized response")
	}
	if !strings.Contains(err.Error(), "unexpectedly large") {
		t.Errorf("error should mention size: %v", err)
	}
}

func TestFetchLatestVersionTrimsWhitespace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "\n  1.74.5\r\n  \n")
	}))
	t.Cleanup(srv.Close)

	got, err := fetchLatestVersionFrom(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.74.5" {
		t.Fatalf("got %q, want %q", got, "1.74.5")
	}
}
