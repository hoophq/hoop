package sidecardemo

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgproto3"
)

// The Postgres demo is the database twin of the HTTP demo in demo.go: the same
// invented users, served this time over the Postgres wire protocol so a person
// with no database at hand can watch the sidecar redact a column and refuse a
// DROP against a real SQL connection. The sidecar in front is what masks and
// guards; this server only produces the rows.
//
// It speaks the simple query protocol only — the one psql uses for typed SQL —
// and answers SELECT <columns> FROM users [WHERE id = n] and INSERT INTO users.
// A client that drives the extended protocol (most drivers, by default) gets a
// clear error, because a demo is for psql, not for an application.

// PGAddr is where the demo Postgres prefers to listen: loopback, behind the
// demo listener that fronts it. It sits just past the HTTP demo's ports
// (18080/18081), so the two demos share one neighbourhood and neither lands on
// 15432, the port the setup screen gives a real Postgres relay. The setup
// screen moves both to free ports when another program holds these.
const PGAddr = "127.0.0.1:18083"

// PGListenAddr is where the demo listener prefers to accept psql clients.
const PGListenAddr = "127.0.0.1:18082"

// PGDefaultPorts are the preferred addresses for the Postgres demo.
var PGDefaultPorts = Ports{Listen: PGListenAddr, API: PGAddr}

// PGSteps is the guided Postgres demo, the SQL twin of Steps: the same four
// beats — see masking, hit a guardrail, write through, compare without the
// sidecar — as statements instead of HTTP requests.
var PGSteps = []Step{
	{
		Title: "See masking", SQL: "SELECT id, name, email FROM users;",
		Expect:   "Every email comes back as [REDACTED:EMAIL_ADDRESS].",
		Why:      "The masking rule \"emails\" found each address in the result and rewrote it before the client saw it.",
		SeenKind: "masked", SeenOp: "select",
	},
	{
		Title: "See a guardrail", SQL: "DROP TABLE users;",
		Expect:   "Refused: the statement never reaches the database.",
		Why:      "The guardrail \"no-destructive-statements\" matched the DROP and the sidecar answered for the database.",
		SeenKind: "violation", SeenOp: "drop",
	},
	{
		Title: "Write through it", SQL: "INSERT INTO users (name, email) VALUES ('Ada Lovelace', 'ada@example.com');",
		Expect:   "INSERT 0 1: the write passes through and is recorded.",
		Why:      "Allowed writes pass through and are recorded in the audit trail like every statement.",
		SeenKind: "statement", SeenOp: "insert",
	},
	{
		Title: "Compare: no sidecar", SQL: "SELECT email FROM users;", Direct: true,
		Expect: "The real email addresses, because this query went around the sidecar.",
		Why:    "This query went straight to the database. Only traffic sent to the sidecar listener is inspected.",
	},
}

// Psql is the step as a psql command to paste into another terminal. A direct
// step connects to the database itself, the rest to the sidecar listener.
func (s Step) Psql(p Ports) string {
	host := p.Listen
	if s.Direct {
		host = p.API
	}
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		h, port = host, "5432"
	}
	return fmt.Sprintf("psql -h %s -p %s -U demo -d postgres -c %q", h, port, s.SQL)
}

// demoColumns are the columns the demo users table reports, in order.
var demoColumns = []string{"id", "name", "email", "phone", "card", "plan"}

// oidText is the Postgres type OID for text. Every column is served as text:
// psql renders it directly, and the masker reads the values as text to redact
// them, so a demo needs no other type.
const oidText = 25

// ServePostgres runs the demo Postgres on addr until ctx ends. Like Serve, a
// port already taken is returned at once: booting a demo whose upstream is
// someone else's database would show that database under the demo's name.
func ServePostgres(ctx context.Context, addr string) (<-chan error, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("the demo Postgres cannot listen on %s: %w", addr, err)
	}
	srv := &pgServer{users: seed()}
	srv.next = len(srv.users) + 1
	done := make(chan error, 1)
	go func() { done <- srv.serve(ctx, ln) }()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	return done, nil
}

// pgServer is the demo database. Its state lives in memory and resets per
// process, like the HTTP demo's.
type pgServer struct {
	mu    sync.Mutex
	users []user
	next  int
}

func (s *pgServer) serve(ctx context.Context, ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

func (s *pgServer) handle(ctx context.Context, conn net.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close()
	// Close the connection when the demo stops, so a client sitting idle at a
	// psql prompt does not keep it open past the sidecar.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	b := pgproto3.NewBackend(conn, conn)
	if !handshake(b, conn) {
		return
	}
	for {
		msg, err := b.Receive()
		if err != nil {
			return
		}
		switch m := msg.(type) {
		case *pgproto3.Query:
			s.answer(b, m.String)
		case *pgproto3.Terminate:
			return
		default:
			fail(b, "the demo Postgres speaks the simple query protocol only; use psql")
		}
		if err := b.Flush(); err != nil {
			return
		}
	}
}

// handshake answers the startup exchange: SSL/GSS probes are declined so the
// connection stays plaintext (it is loopback), then the StartupMessage is
// accepted with no password and the parameters psql expects. It reports
// whether the connection is ready for queries.
func handshake(b *pgproto3.Backend, conn net.Conn) bool {
	for {
		msg, err := b.ReceiveStartupMessage()
		if err != nil {
			return false
		}
		switch msg.(type) {
		case *pgproto3.SSLRequest, *pgproto3.GSSEncRequest:
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return false
			}
			continue
		case *pgproto3.StartupMessage:
		default:
			return false
		}
		break
	}

	b.Send(&pgproto3.AuthenticationOk{})
	for _, kv := range [][2]string{
		{"server_version", "16.0 (hoop sidecar demo)"},
		{"client_encoding", "UTF8"},
		{"server_encoding", "UTF8"},
		{"DateStyle", "ISO, MDY"},
		{"standard_conforming_strings", "on"},
		{"integer_datetimes", "on"},
		{"TimeZone", "UTC"},
	} {
		b.Send(&pgproto3.ParameterStatus{Name: kv[0], Value: kv[1]})
	}
	b.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 0}})
	b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	return b.Flush() == nil
}

// answer replies to one simple-query statement.
func (s *pgServer) answer(b *pgproto3.Backend, sql string) {
	q := strings.ToLower(strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), ";")))
	switch {
	case q == "":
		b.Send(&pgproto3.EmptyQueryResponse{})
	case strings.HasPrefix(q, "select"):
		cols, id, err := parseSelect(q)
		if err != nil {
			failCode(b, err.code, err.msg)
			return
		}
		s.sendUsers(b, cols, id)
		return
	case strings.HasPrefix(q, "insert"):
		s.insert(b, sql)
		return
	default:
		// DROP, DELETE and TRUNCATE never reach here through the sidecar: a
		// guardrail refuses them first. A client connected straight to the
		// demo still gets a clean acknowledgement rather than a dropped socket.
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("OK")})
	}
	b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
}

// pgError is a statement the demo cannot answer, with the SQLSTATE a real
// Postgres would report for it.
type pgError struct{ code, msg string }

// parseSelect reads the one SELECT shape the demo answers:
//
//	SELECT <* | column, ...> FROM users [WHERE id = <n>]
//
// q is already lowercased, trimmed and stripped of its semicolon. It returns
// the columns asked for, in order, and the id filter (0 for none). Anything
// else is an error a person at psql can act on, never a silently wrong answer:
// a demo about what reaches the client must not invent columns nobody asked for.
func parseSelect(q string) ([]string, int, *pgError) {
	shape := &pgError{"0A000", "the demo database answers SELECT <columns> FROM users [WHERE id = <n>]"}
	rest := strings.TrimSpace(strings.TrimPrefix(q, "select"))
	i := strings.Index(rest, " from ")
	if i < 0 {
		return nil, 0, shape
	}
	colPart := strings.TrimSpace(rest[:i])
	table, where, _ := strings.Cut(strings.TrimSpace(rest[i+len(" from "):]), " ")
	if table != "users" {
		return nil, 0, &pgError{"42P01", fmt.Sprintf("relation %q does not exist; the demo database has one table, users", table)}
	}

	id := 0
	if where = strings.ReplaceAll(where, " ", ""); where != "" {
		n, ok := strings.CutPrefix(where, "whereid=")
		if !ok {
			return nil, 0, shape
		}
		v, err := strconv.Atoi(n)
		if err != nil {
			return nil, 0, shape
		}
		id = v
	}

	if colPart == "*" {
		return demoColumns, id, nil
	}
	var cols []string
	for _, c := range strings.Split(colPart, ",") {
		c = strings.TrimSpace(c)
		if !slices.Contains(demoColumns, c) {
			return nil, 0, &pgError{"42703", fmt.Sprintf("column %q does not exist", c)}
		}
		cols = append(cols, c)
	}
	return cols, id, nil
}

// column is one user's value for a demo column.
func (u user) column(c string) string {
	switch c {
	case "id":
		return strconv.Itoa(u.ID)
	case "name":
		return u.Name
	case "email":
		return u.Email
	case "phone":
		return u.Phone
	case "card":
		return u.Card
	case "plan":
		return u.Plan
	}
	return ""
}

// sendUsers returns the users table, projected to cols and filtered to id
// when it is not 0. The sidecar's mask rule rewrites the sensitive columns on
// the way back; this server always sends the real values, so the "no sidecar"
// comparison step can show them.
func (s *pgServer) sendUsers(b *pgproto3.Backend, cols []string, id int) {
	fields := make([]pgproto3.FieldDescription, len(cols))
	for i, c := range cols {
		fields[i] = pgproto3.FieldDescription{
			Name: []byte(c), DataTypeOID: oidText, DataTypeSize: -1,
			TypeModifier: -1, Format: pgproto3.TextFormat,
		}
	}
	b.Send(&pgproto3.RowDescription{Fields: fields})

	s.mu.Lock()
	users := append([]user(nil), s.users...)
	s.mu.Unlock()
	n := 0
	for _, u := range users {
		if id != 0 && u.ID != id {
			continue
		}
		values := make([][]byte, len(cols))
		for i, c := range cols {
			values[i] = []byte(u.column(c))
		}
		b.Send(&pgproto3.DataRow{Values: values})
		n++
	}
	b.Send(&pgproto3.CommandComplete{CommandTag: []byte(fmt.Sprintf("SELECT %d", n))})
	b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
}

// insert adds one user so a later SELECT shows the write landed, then
// acknowledges it the way Postgres does. The name and email are read from the
// statement when it is the simple form the demo suggests; anything fancier is
// still acknowledged, because the sidecar already allowed it.
func (s *pgServer) insert(b *pgproto3.Backend, sql string) {
	if name, email, ok := parseInsert(sql); ok {
		s.mu.Lock()
		s.users = append(s.users, user{ID: s.next, Name: name, Email: email, Plan: "free"})
		s.next++
		s.mu.Unlock()
	}
	b.Send(&pgproto3.CommandComplete{CommandTag: []byte("INSERT 0 1")})
	b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
}

// parseInsert pulls the first two single-quoted values out of an INSERT as the
// name and email. It is deliberately small: the demo suggests one exact
// statement, and a value it cannot read is not worth refusing a write the
// sidecar allowed.
func parseInsert(sql string) (name, email string, ok bool) {
	i := strings.Index(strings.ToLower(sql), "values")
	if i < 0 {
		return "", "", false
	}
	vals := quoted(sql[i:])
	if len(vals) < 2 {
		return "", "", false
	}
	return vals[0], vals[1], true
}

// quoted returns the single-quoted strings in s, in order, with ” read as a
// literal quote.
func quoted(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '\'')
		if i < 0 {
			return out
		}
		s = s[i+1:]
		var b strings.Builder
		for {
			j := strings.IndexByte(s, '\'')
			if j < 0 {
				return out
			}
			if j+1 < len(s) && s[j+1] == '\'' {
				b.WriteString(s[:j])
				b.WriteByte('\'')
				s = s[j+2:]
				continue
			}
			b.WriteString(s[:j])
			s = s[j+1:]
			break
		}
		out = append(out, b.String())
	}
}

// fail answers one statement with a "feature not supported" error.
func fail(b *pgproto3.Backend, msg string) { failCode(b, "0A000", msg) }

// failCode answers one statement with a Postgres error under the given
// SQLSTATE and returns to ready, so the client stays connected.
func failCode(b *pgproto3.Backend, code, msg string) {
	b.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: code, Message: msg})
	b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
}
