package sidecardemo

import (
	"context"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

// freePort returns a loopback address nothing is listening on. The small gap
// between closing it and ServePostgres binding it is good enough for a test.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// dialReady connects, completes the startup handshake, and returns a frontend
// ready to send queries.
func dialReady(t *testing.T, addr string) (*pgproto3.Frontend, net.Conn) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fe := pgproto3.NewFrontend(conn, conn)
	fe.Send(&pgproto3.StartupMessage{
		ProtocolVersion: pgproto3.ProtocolVersionNumber,
		Parameters:      map[string]string{"user": "demo", "database": "postgres"},
	})
	if err := fe.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, ok := recvUntilReady(t, fe); !ok {
		t.Fatal("no ReadyForQuery after startup")
	}
	return fe, conn
}

// recvUntilReady reads backend messages until ReadyForQuery, returning the
// data rows seen (as strings) and whether it got there.
func recvUntilReady(t *testing.T, fe *pgproto3.Frontend) ([][]string, bool) {
	t.Helper()
	var rows [][]string
	for {
		msg, err := fe.Receive()
		if err != nil {
			return rows, false
		}
		switch m := msg.(type) {
		case *pgproto3.DataRow:
			row := make([]string, len(m.Values))
			for i, v := range m.Values {
				row[i] = string(v)
			}
			rows = append(rows, row)
		case *pgproto3.ErrorResponse:
			t.Fatalf("server error: %s", m.Message)
		case *pgproto3.ReadyForQuery:
			return rows, true
		}
	}
}

func TestPostgresDemoServesUsers(t *testing.T) {
	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := ServePostgres(ctx, addr); err != nil {
		t.Fatal(err)
	}

	fe, conn := dialReady(t, addr)
	defer conn.Close()

	// SELECT returns the whole seeded table, with the real email addresses the
	// sidecar would otherwise redact.
	fe.Send(&pgproto3.Query{String: "SELECT * FROM users;"})
	if err := fe.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, ok := recvUntilReady(t, fe)
	if !ok {
		t.Fatal("no ReadyForQuery after SELECT")
	}
	if len(rows) != len(seed()) {
		t.Fatalf("got %d rows, want %d", len(rows), len(seed()))
	}
	if len(rows[0]) != len(demoColumns) {
		t.Fatalf("got %d columns, want %d", len(rows[0]), len(demoColumns))
	}
	if rows[0][2] != "grace.hopper@example.com" {
		t.Errorf("email column = %q, want the seeded address", rows[0][2])
	}

	// INSERT is acknowledged and lands, so the next SELECT shows one more row.
	fe.Send(&pgproto3.Query{String: "INSERT INTO users (name, email) VALUES ('Ada Lovelace', 'ada@example.com');"})
	if err := fe.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, ok := recvUntilReady(t, fe); !ok {
		t.Fatal("no ReadyForQuery after INSERT")
	}
	fe.Send(&pgproto3.Query{String: "SELECT * FROM users;"})
	if err := fe.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, _ = recvUntilReady(t, fe)
	if len(rows) != len(seed())+1 {
		t.Fatalf("after INSERT got %d rows, want %d", len(rows), len(seed())+1)
	}
	last := rows[len(rows)-1]
	if last[1] != "Ada Lovelace" || last[2] != "ada@example.com" {
		t.Errorf("inserted row = %v, want Ada Lovelace / ada@example.com", last)
	}
}

// The demo answers only what was asked: the columns named, in their order,
// and the row an id filter picks. A shape it does not know is an error with
// the SQLSTATE Postgres would use, never a table nobody asked for.
func TestPostgresDemoParseSelect(t *testing.T) {
	for _, c := range []struct {
		q    string
		cols []string
		id   int
		code string
	}{
		{q: "select * from users", cols: demoColumns},
		{q: "select id, name, email from users", cols: []string{"id", "name", "email"}},
		{q: "select email from users where id = 2", cols: []string{"email"}, id: 2},
		{q: "select email from users where id=2", cols: []string{"email"}, id: 2},
		{q: "select password from users", code: "42703"},
		{q: "select * from orders", code: "42P01"},
		{q: "select 1", code: "0A000"},
		{q: "select * from users where name = 'x'", code: "0A000"},
	} {
		cols, id, err := parseSelect(c.q)
		if c.code != "" {
			if err == nil || err.code != c.code {
				t.Errorf("%q: err = %v, want SQLSTATE %s", c.q, err, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %s", c.q, err.msg)
			continue
		}
		if !slices.Equal(cols, c.cols) || id != c.id {
			t.Errorf("%q: cols %v id %d, want %v id %d", c.q, cols, id, c.cols, c.id)
		}
	}
}

func TestPostgresDemoParseInsert(t *testing.T) {
	name, email, ok := parseInsert("INSERT INTO users (name, email) VALUES ('Ada ''Countess'' Lovelace', 'ada@example.com')")
	if !ok || name != "Ada 'Countess' Lovelace" || email != "ada@example.com" {
		t.Fatalf("parseInsert = %q, %q, %v", name, email, ok)
	}
}
