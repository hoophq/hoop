package router

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestConnect(t *testing.T) {
	twin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "twin") }))
	defer twin.Close()
	r, err := New([]string{"oidc-twin=" + twin.Listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(r.Handler())
	defer proxy.Close()

	// The twin's certificate names example.com; the client checks it end to
	// end through the tunnel.
	tr := twin.Client().Transport.(*http.Transport).Clone()
	proxyURL, _ := url.Parse(proxy.URL)
	tr.Proxy = http.ProxyURL(proxyURL)
	tr.TLSClientConfig.ServerName = "example.com"
	client := &http.Client{Transport: tr}

	resp, err := client.Get("https://oidc-twin/x")
	if err != nil {
		t.Fatalf("mapped host: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "twin" {
		t.Fatalf("mapped host answered %q", b)
	}

	if _, err := client.Get("https://api.example.org/x"); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("unmapped host: got err=%v, want the router to refuse it", err)
	}

	resp, err = http.Get(proxy.URL + "/_twin/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		State struct {
			Tunnels []Tunnel `json:"tunnels"`
			Refused []struct {
				Host  string `json:"host"`
				Count int    `json:"count"`
			} `json:"refused"`
		} `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if len(st.State.Tunnels) != 2 || !st.State.Tunnels[0].Allowed || st.State.Tunnels[1].Allowed {
		t.Fatalf("tunnels: %+v", st.State.Tunnels)
	}
	if len(st.State.Refused) != 1 || st.State.Refused[0].Host != "api.example.org:443" || st.State.Refused[0].Count != 1 {
		t.Fatalf("refused: %+v", st.State.Refused)
	}
}

// A twin that resets the connection mid-tunnel shows as an error in
// /_twin/state, not as a clean close.
func TestTunnelErrorIsRecorded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		io.ReadFull(c, make([]byte, 5))
		c.(*net.TCPConn).SetLinger(0) // close with RST
		c.Close()
	}()
	r, _ := New([]string{"oidc-twin=" + ln.Addr().String()})
	proxy := httptest.NewServer(r.Handler())
	defer proxy.Close()

	conn, err := net.Dial("tcp", proxy.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "CONNECT oidc-twin:443 HTTP/1.1\r\nHost: oidc-twin:443\r\n\r\n")
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.Contains(reply, "200") {
		t.Fatalf("CONNECT reply %q, %v", reply, err)
	}
	io.WriteString(conn, "hello")

	deadline := time.Now().Add(5 * time.Second)
	for {
		st := r.State().(map[string]any)
		if tunnels := st["tunnels"].([]Tunnel); len(tunnels) == 1 && tunnels[0].Error != "" {
			if !tunnels[0].Allowed {
				t.Fatalf("tunnel: %+v", tunnels[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reset was not recorded: %+v", st["tunnels"])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBadRoute(t *testing.T) {
	if _, err := New([]string{"no-target"}); err == nil {
		t.Fatal("a route with no target must fail")
	}
}
