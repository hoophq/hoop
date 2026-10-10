package descriptors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPSIsLinkedInEveryBuild(t *testing.T) {
	if !Linked("https") {
		t.Fatal("the root module did not register the https fetcher")
	}
	if Linked("http") {
		t.Fatal("plain http is registered; a plug-in over an unauthenticated transport is a supply-chain hole")
	}
}

func TestHTTPSFetchesThroughTheGivenClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.pb":
			w.Write([]byte("descriptor bytes"))
		case "/missing.pb":
			http.Error(w, "no such object", http.StatusNotFound)
		case "/huge.wasm":
			w.Header().Set("Content-Length", "")
			chunk := make([]byte, 1<<20)
			for i := 0; i <= MaxHTTPSBytes>>20; i++ {
				w.Write(chunk)
			}
		}
	}))
	defer srv.Close()

	// The server's own client trusts its certificate; the stock client does
	// not, which is the point: the fetch rides the client it is handed.
	blob, err := Fetch(context.Background(), srv.URL+"/ok.pb", srv.Client())
	if err != nil || string(blob) != "descriptor bytes" {
		t.Fatalf("fetch = %q, %v", blob, err)
	}
	if _, err := Fetch(context.Background(), srv.URL+"/ok.pb", &http.Client{}); err == nil {
		t.Fatal("a client without the trust root fetched")
	}

	_, err = Fetch(context.Background(), srv.URL+"/missing.pb", srv.Client())
	if err == nil {
		t.Fatal("a 404 fetched")
	}
	for _, want := range []string{"404", "no such object"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}

	_, err = Fetch(context.Background(), srv.URL+"/huge.wasm", srv.Client())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("a body past the bound was read whole: %v", err)
	}
}
