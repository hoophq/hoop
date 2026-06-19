package transport

import (
	"bufio"
	"bytes"
	"net/http"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/aianalyzer"
)

func TestHTTPAnalyzerStore(t *testing.T) {
	s := &httpAnalyzerStore{sessions: map[string]map[string]*httpConnAnalysis{}}

	a := s.conn("sid-1", "1")
	if a == nil {
		t.Fatal("expected a connection state")
	}
	// Same key returns the same instance, so decided/blocked persist per connection.
	a.decided = true
	a.blocked = true
	if again := s.conn("sid-1", "1"); again != a || !again.decided || !again.blocked {
		t.Fatal("expected the same persisted connection state instance")
	}
	// Different connection id is independent.
	if other := s.conn("sid-1", "2"); other.decided || other.blocked {
		t.Fatal("expected a fresh state for a different connection id")
	}

	s.dropSession("sid-1")
	if fresh := s.conn("sid-1", "1"); fresh.decided || fresh.blocked {
		t.Fatal("expected state to be cleared after dropSession")
	}
}

func TestHTTPProxyForbiddenPacket(t *testing.T) {
	d := &aianalyzer.HTTPDecision{
		Outcome:     aianalyzer.OutcomeBlock,
		RiskLevel:   aianalyzer.RiskLevelHigh,
		Title:       "drop table",
		Explanation: "destructive sql",
		RuleName:    "rule-1",
	}
	pkt := httpProxyForbiddenPacket("sid-1", "7", d)

	if pkt.Type != pbclient.HttpProxyConnectionWrite {
		t.Fatalf("unexpected packet type: %v", pkt.Type)
	}
	if string(pkt.Spec[pb.SpecGatewaySessionID]) != "sid-1" || string(pkt.Spec[pb.SpecClientConnectionID]) != "7" {
		t.Fatalf("unexpected packet spec routing: %v", pkt.Spec)
	}

	// Payload must be a well-formed HTTP 403 the client proxy can write verbatim.
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(pkt.Payload)), nil)
	if err != nil {
		t.Fatalf("payload is not a valid HTTP response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Connection"); got != "close" {
		t.Fatalf("expected Connection: close, got %q", got)
	}
}
