package sidecartui

import (
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

// login is what a client sent to fakePostgres before its first query.
type login struct{ user, database, password string }

// fakePostgres serves one connection on loopback the way a password-protected
// Postgres would: it asks for a cleartext password, records the login, then
// answers the first query with reply. It stands in for the sidecar or the
// demo database, so the tour's client is tested without either.
func fakePostgres(t *testing.T, reply func(b *pgproto3.Backend, conn net.Conn)) (string, <-chan login) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan login, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		b := pgproto3.NewBackend(conn, conn)
		msg, err := b.ReceiveStartupMessage()
		if err != nil {
			return
		}
		sm, ok := msg.(*pgproto3.StartupMessage)
		if !ok {
			return
		}
		b.Send(&pgproto3.AuthenticationCleartextPassword{})
		if b.Flush() != nil || b.SetAuthType(pgproto3.AuthTypeCleartextPassword) != nil {
			return
		}
		pw, err := b.Receive()
		if err != nil {
			return
		}
		p, _ := pw.(*pgproto3.PasswordMessage)
		got <- login{sm.Parameters["user"], sm.Parameters["database"], p.Password}
		b.Send(&pgproto3.AuthenticationOk{})
		// What every real server reports, RDS included; pgx refuses the
		// simple protocol without standard_conforming_strings.
		b.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "16.0"})
		b.Send(&pgproto3.ParameterStatus{Name: "client_encoding", Value: "UTF8"})
		b.Send(&pgproto3.ParameterStatus{Name: "standard_conforming_strings", Value: "on"})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if b.Flush() != nil {
			return
		}
		if _, err := b.Receive(); err != nil {
			return
		}
		reply(b, conn)
	}()
	return ln.Addr().String(), got
}

// The tour connects as the demo's read-only role, and a query's rows come back
// as a header, a rule and one line per row.
func TestPgQueryLogsInAsTheDemoRoleAndRendersRows(t *testing.T) {
	addr, got := fakePostgres(t, func(b *pgproto3.Backend, _ net.Conn) {
		text := func(name string) pgproto3.FieldDescription {
			return pgproto3.FieldDescription{Name: []byte(name), DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1}
		}
		b.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{text("customerid"), text("email")}})
		b.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("1"), []byte("[REDACTED:EMAIL_ADDRESS]")}})
		b.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("2"), nil}})
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 2")})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		_ = b.Flush()
	})

	r, err := pgQuery(pgTarget{addr: addr}, "SELECT customerid, email FROM customers LIMIT 2;")
	if err != nil {
		t.Fatal(err)
	}
	if l := <-got; l.user != sidecardemo.PGUser || l.database != sidecardemo.PGDatabase || l.password != sidecardemo.PGPassword {
		t.Errorf("logged in as %+v, want the demo role on %s", l, sidecardemo.PGDatabase)
	}
	lines := strings.Split(r.body, "\n")
	if r.status != 200 || len(lines) != 4 || !strings.HasPrefix(lines[0], "customerid") ||
		!strings.Contains(lines[2], "[REDACTED:EMAIL_ADDRESS]") || !strings.Contains(lines[3], "NULL") {
		t.Errorf("status %d body:\n%s\nwant a header, a rule and two rows", r.status, r.body)
	}
}

// A guardrail denial ends the connection instead of returning to ready. The
// tour must read that as the step's answer — refused, with the guardrail's
// message — and not as a broken connection.
func TestPgQueryReadsTheSidecarRefusal(t *testing.T) {
	const msg = "DROP and TRUNCATE are not allowed through the sidecar"
	addr, _ := fakePostgres(t, func(_ *pgproto3.Backend, conn net.Conn) {
		_, _ = conn.Write(proxy.PostgresError(msg))
	})
	r, err := pgQuery(pgTarget{addr: addr}, "DROP TABLE customers;")
	if err != nil {
		t.Fatalf("a refusal is an answer, not an error: %v", err)
	}
	if r.status != 403 || r.body != msg {
		t.Errorf("status %d body %q, want 403 and the guardrail's message", r.status, r.body)
	}
}

// Any other Postgres error is the database's answer, not the sidecar's: it
// keeps its message and is not called a refusal.
func TestPgQueryShowsADatabaseErrorAsIs(t *testing.T) {
	addr, _ := fakePostgres(t, func(b *pgproto3.Backend, _ net.Conn) {
		b.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "42703", Message: `column "nope" does not exist`})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		_ = b.Flush()
	})
	r, err := pgQuery(pgTarget{addr: addr}, "SELECT nope FROM customers;")
	if err != nil {
		t.Fatal(err)
	}
	tr := newTour(&DemoOptions{Kind: sidecardemo.KindPostgres})
	if r.status != 400 || !strings.Contains(r.body, "does not exist") || tr.statusText(r.status) != "error" {
		t.Errorf("status %d (%s) body %q, want 400 error with the database's message", r.status, tr.statusText(r.status), r.body)
	}
}

// A psql pasted into another terminal ticks the Postgres steps from the audit
// trail. The events are the sequence the Postgres lane records: the client's
// query, the server's CommandComplete echoed as a statement with operation
// unknown, then the masked rows. The safe query is a SELECT too, and only its
// table tells it from the masking step's.
func TestPostgresTourTicksStepsFromTheAuditTrail(t *testing.T) {
	tr := newTour(&DemoOptions{Kind: sidecardemo.KindPostgres})
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "select", Tables: []string{"customers"}, SessionID: "s1", Direction: inspect.FromClient})
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "unknown", SessionID: "s1", Direction: inspect.FromServer})
	tr.see(audit.Event{Kind: "masked", SessionID: "s1", Direction: inspect.FromServer})
	if !tr.done[0] || tr.done[2] {
		t.Fatalf("after the masking query: done = %v, want See masking only", tr.done)
	}
	tr.see(audit.Event{Kind: "violation", Operation: "drop", Tables: []string{"customers"}, SessionID: "s2", Direction: inspect.FromClient})
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "select", Tables: []string{"products"}, SessionID: "s3", Direction: inspect.FromClient})
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
	view := tr.view(200, 40, time.Now())
	if !strings.Contains(view, "psql") || !strings.Contains(view, "port=18082") {
		t.Errorf("view does not show the psql command for the listener:\n%s", view)
	}
	if _, used := tr.key(key("o")); !used || !strings.Contains(tr.flash, "psql") {
		t.Errorf("o on the Postgres tour: flash %q, want a pointer to the psql command", tr.flash)
	}
}
