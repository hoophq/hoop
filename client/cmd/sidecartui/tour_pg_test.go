package sidecartui

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// refusingPostgres accepts one connection, completes the startup, and answers
// the first query the way the sidecar refuses one: proxy.PostgresError's
// FATAL ErrorResponse, then a closed socket with no ReadyForQuery.
func refusingPostgres(t *testing.T, msg string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		b := pgproto3.NewBackend(conn, conn)
		if _, err := b.ReceiveStartupMessage(); err != nil {
			return
		}
		b.Send(&pgproto3.AuthenticationOk{})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if b.Flush() != nil {
			return
		}
		if _, err := b.Receive(); err != nil {
			return
		}
		_, _ = conn.Write(proxy.PostgresError(msg))
	}()
	return ln.Addr().String()
}

// A guardrail denial ends the connection instead of returning to ready. The
// tour must read that as the step's answer — refused, with the guardrail's
// message — and not as a broken connection.
func TestPgQueryReadsARefusalThatClosesTheConnection(t *testing.T) {
	const msg = "DROP and TRUNCATE are not allowed through the sidecar"
	r, err := pgQuery(refusingPostgres(t, msg), "DROP TABLE users;")
	if err != nil {
		t.Fatalf("a refusal is an answer, not an error: %v", err)
	}
	if r.status != 403 || r.body != msg {
		t.Errorf("status %d body %q, want 403 and the guardrail's message", r.status, r.body)
	}
}

// Every step runs against the real demo database and comes back with rows or
// a command tag, through the same client "run it here" uses.
func TestPgQueryAgainstTheDemoDatabase(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := sidecardemo.ServePostgres(ctx, addr); err != nil {
		t.Fatal(err)
	}

	r, err := pgQuery(addr, "SELECT id, email FROM users WHERE id = 1;")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(r.body, "\n")
	if r.status != 200 || len(lines) != 3 || !strings.HasPrefix(lines[0], "id") ||
		!strings.Contains(lines[2], "grace.hopper@example.com") {
		t.Errorf("status %d body:\n%s\nwant a header, a rule and Grace's row", r.status, r.body)
	}

	r, err = pgQuery(addr, "INSERT INTO users (name, email) VALUES ('Ada Lovelace', 'ada@example.com');")
	if err != nil || r.status != 200 || r.body != "INSERT 0 1" {
		t.Errorf("INSERT: status %d body %q err %v", r.status, r.body, err)
	}
}

// A psql pasted into another terminal ticks the Postgres steps from the audit
// trail. The events are the sequence the Postgres lane records: the client's
// query, the server's CommandComplete echoed as a statement with operation
// unknown, then the masked rows.
func TestPostgresTourTicksStepsFromTheAuditTrail(t *testing.T) {
	tr := newTour(&DemoOptions{Kind: sidecardemo.KindPostgres})
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "select", SessionID: "s1", Direction: inspect.FromClient})
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "unknown", SessionID: "s1", Direction: inspect.FromServer})
	tr.see(audit.Event{Kind: "masked", SessionID: "s1", Direction: inspect.FromServer})
	tr.see(audit.Event{Kind: "violation", Operation: "drop", SessionID: "s2", Direction: inspect.FromClient})
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "insert", SessionID: "s3", Direction: inspect.FromClient})
	for i := 0; i < 3; i++ {
		if !tr.done[i] {
			t.Errorf("step %d (%s) not ticked", i+1, tr.steps[i].Title)
		}
	}
	if tr.done[3] {
		t.Error("the direct query was ticked: the sidecar never sees it")
	}
}

// The Postgres tour offers psql, never a browser, and copies psql.
func TestPostgresTourOffersPsqlNotABrowser(t *testing.T) {
	tr := newTour(&DemoOptions{Kind: sidecardemo.KindPostgres, OpenURL: func(string) error { return nil }})
	var labels []string
	for _, h := range tr.hints() {
		labels = append(labels, h[1])
	}
	joined := strings.Join(labels, ",")
	if !strings.Contains(joined, "copy psql") || strings.Contains(joined, "browser") {
		t.Errorf("hints = %v, want copy psql and no browser", labels)
	}
	view := tr.view(120, 40, time.Now())
	if !strings.Contains(view, "psql -h 127.0.0.1 -p 18082") {
		t.Errorf("view does not show the psql command:\n%s", view)
	}
	if _, used := tr.key(key("o")); !used || !strings.Contains(tr.flash, "psql") {
		t.Errorf("o on the Postgres tour: flash %q, want a pointer to the psql command", tr.flash)
	}
}
