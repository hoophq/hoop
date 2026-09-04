package inspect_test

import (
	"errors"
	"testing"

	_ "github.com/hoophq/hoop/sidecar/codec/all"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestNewUnsupportedProtocol(t *testing.T) {
	_, err := inspect.New("oracle")
	if !errors.Is(err, inspect.ErrUnsupportedProtocol) {
		t.Errorf("err = %v, want ErrUnsupportedProtocol", err)
	}
}

func TestRegisteredCoversAllShippedProtocols(t *testing.T) {
	got := map[inspect.Protocol]bool{}
	for _, p := range inspect.Registered() {
		got[p] = true
	}
	for _, want := range []inspect.Protocol{
		inspect.Postgres, inspect.MSSQL, inspect.MySQL, inspect.MongoDB, inspect.HTTP,
	} {
		if !got[want] {
			t.Errorf("protocol %q is not registered by codec/all", want)
		}
	}
}

// A Protocol constant with no codec behind it is a promise the library
// cannot keep. Registered() is the honest list, so it must not grow a name
// that New would then reject.
func TestEveryRegisteredProtocolConstructs(t *testing.T) {
	for _, p := range inspect.Registered() {
		if _, err := inspect.New(p); err != nil {
			t.Errorf("New(%q) failed though the protocol is registered: %v", p, err)
		}
	}
}

func TestNewReturnsDistinctCodecs(t *testing.T) {
	// Codecs must not be shared across connections: a stateful one would let
	// two connections corrupt each other's reassembly buffer. The codec
	// packages assert the behavioral version; this asserts the registry
	// contract that makes it possible.
	a, err := inspect.New(inspect.Postgres)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := inspect.New(inspect.Postgres)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == b {
		t.Fatal("New returned the same Inspector twice")
	}
}

// --- a stub codec, to exercise Inspector without any real protocol

type stubCodec struct {
	// consume tells Decode how many bytes to report consuming.
	consume func(data []byte) (int, error)
	seen    [][]byte
}

func (s *stubCodec) Protocol() inspect.Protocol { return "stub" }

func (s *stubCodec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.seen = append(s.seen, cp)

	n, err := s.consume(data)
	if err != nil {
		return nil, n, err
	}
	if n == 0 {
		return nil, 0, nil
	}
	return []inspect.Statement{{
		Protocol:  "stub",
		Direction: dir,
		Text:      string(data[:n]),
	}}, n, nil
}

func TestInspectorRetainsUndecodedTail(t *testing.T) {
	// Consume only complete 4-byte units.
	c := &stubCodec{consume: func(d []byte) (int, error) {
		return len(d) / 4 * 4, nil
	}}
	insp := inspect.NewWithCodec(c)

	if _, err := insp.Inspect(inspect.FromClient, []byte("abcdef")); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Buffered() != 2 {
		t.Errorf("Buffered = %d, want 2", insp.Buffered())
	}

	if _, err := insp.Inspect(inspect.FromClient, []byte("gh")); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Buffered() != 0 {
		t.Errorf("Buffered = %d, want 0", insp.Buffered())
	}
	// The second decode must have seen the retained "ef" prepended.
	last := c.seen[len(c.seen)-1]
	if string(last) != "efgh" {
		t.Errorf("codec saw %q on the second call, want %q", last, "efgh")
	}
}

func TestInspectorDropsBufferOnError(t *testing.T) {
	boom := errors.New("boom")
	c := &stubCodec{consume: func(d []byte) (int, error) { return 0, boom }}
	insp := inspect.NewWithCodec(c)

	if _, err := insp.Inspect(inspect.FromClient, []byte("xyz")); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if insp.Buffered() != 0 {
		t.Errorf("Buffered = %d after an error, want 0. A caller that ignores "+
			"the error must not keep re-decoding the same garbage", insp.Buffered())
	}
}

// A peer that never completes a message must not pin unbounded memory.
func TestBufferOverflow(t *testing.T) {
	c := &stubCodec{consume: func(d []byte) (int, error) { return 0, nil }}
	insp := inspect.NewWithCodec(c)
	insp.SetMaxBuffer(64)

	var err error
	for range 10 {
		_, err = insp.Inspect(inspect.FromClient, make([]byte, 32))
		if err != nil {
			break
		}
	}
	if !errors.Is(err, inspect.ErrBufferOverflow) {
		t.Fatalf("err = %v, want ErrBufferOverflow", err)
	}
	if insp.Buffered() != 0 {
		t.Errorf("Buffered = %d after overflow, want 0", insp.Buffered())
	}
}

// A large input that DOES decode is fine; only a stalled partial message trips
// the overflow guard.
func TestLargeButDecodableInputIsNotAnOverflow(t *testing.T) {
	c := &stubCodec{consume: func(d []byte) (int, error) { return len(d), nil }}
	insp := inspect.NewWithCodec(c)
	insp.SetMaxBuffer(64)

	if _, err := insp.Inspect(inspect.FromClient, make([]byte, 4096)); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Buffered() != 0 {
		t.Errorf("Buffered = %d, want 0", insp.Buffered())
	}
}

func TestSetMaxBufferZeroRestoresDefault(t *testing.T) {
	c := &stubCodec{consume: func(d []byte) (int, error) { return 0, nil }}
	insp := inspect.NewWithCodec(c)
	insp.SetMaxBuffer(16)
	insp.SetMaxBuffer(0)

	// 1 KiB is far above the 16-byte cap but far below the default, so it
	// only succeeds if the default was restored.
	if _, err := insp.Inspect(inspect.FromClient, make([]byte, 1024)); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
}

func TestReset(t *testing.T) {
	c := &stubCodec{consume: func(d []byte) (int, error) { return 0, nil }}
	insp := inspect.NewWithCodec(c)

	insp.Inspect(inspect.FromClient, []byte("partial"))
	if insp.Buffered() == 0 {
		t.Fatal("expected buffered bytes")
	}
	insp.Reset()
	if insp.Buffered() != 0 {
		t.Errorf("Buffered = %d after Reset, want 0", insp.Buffered())
	}
}

func TestEmptyInputIsNoop(t *testing.T) {
	c := &stubCodec{consume: func(d []byte) (int, error) { return len(d), nil }}
	insp := inspect.NewWithCodec(c)

	stmts, err := insp.Inspect(inspect.FromClient, nil)
	if err != nil || len(stmts) != 0 {
		t.Errorf("Inspect(nil) = (%v, %v), want (nil, nil)", stmts, err)
	}
	if len(c.seen) != 0 {
		t.Error("codec was called with no data")
	}
}

func TestProtocolAccessor(t *testing.T) {
	insp, err := inspect.New(inspect.Postgres)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if insp.Protocol() != inspect.Postgres {
		t.Errorf("Protocol = %q, want postgres", insp.Protocol())
	}
}

func TestRegisterRejectsNilFactory(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Register(nil) did not panic")
		}
	}()
	inspect.Register(nil)
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register did not panic")
		}
	}()
	// Postgres is already registered via codec/all.
	inspect.Register(func() inspect.Codec { return &stubPostgres{} })
}

type stubPostgres struct{}

func (stubPostgres) Protocol() inspect.Protocol { return inspect.Postgres }
func (stubPostgres) Decode(inspect.Direction, []byte) ([]inspect.Statement, int, error) {
	return nil, 0, nil
}

// --- classifier ----------------------------------------------------------

func TestClassifySQL(t *testing.T) {
	tests := []struct {
		sql     string
		wantOp  inspect.Operation
		wantTbl []string
	}{
		{"SELECT * FROM t", inspect.OpSelect, []string{"t"}},
		{"select * from T", inspect.OpSelect, []string{"t"}},
		{"INSERT INTO a SELECT * FROM b", inspect.OpInsert, []string{"a", "b"}},
		{"UPDATE a SET x = 1 FROM b", inspect.OpUpdate, []string{"a", "b"}},
		{"BEGIN", inspect.OpBegin, nil},
		{"COMMIT", inspect.OpCommit, nil},
		{"ROLLBACK", inspect.OpRollback, nil},
		{"START TRANSACTION", inspect.OpBegin, nil},
		{"SHOW TABLES", inspect.OpShow, nil},
		// GRANT rewrites the ACL of the named relation, so the relation
		// is reported now. The old lexer had no introducer for ON and
		// found nothing here.
		{"GRANT SELECT ON t TO alice", inspect.OpGrant, []string{"t"}},
		{"", inspect.OpUnknown, nil},
		{"   ", inspect.OpUnknown, nil},
		{"not sql at all", inspect.OpUnknown, nil},
	}
	for _, tc := range tests {
		t.Run(tc.sql, func(t *testing.T) {
			op, tables := inspect.ClassifySQL(tc.sql)
			if op != tc.wantOp {
				t.Errorf("Operation = %q, want %q", op, tc.wantOp)
			}
			if len(tables) != len(tc.wantTbl) {
				t.Fatalf("Tables = %v, want %v", tables, tc.wantTbl)
			}
			for i := range tables {
				if tables[i] != tc.wantTbl[i] {
					t.Errorf("Tables = %v, want %v", tables, tc.wantTbl)
					break
				}
			}
		})
	}
}

func TestClassifyDeduplicatesTables(t *testing.T) {
	_, tables := inspect.ClassifySQL("SELECT * FROM t JOIN t AS t2 ON true")
	if len(tables) != 1 || tables[0] != "t" {
		t.Errorf("Tables = %v, want [t]", tables)
	}
}
