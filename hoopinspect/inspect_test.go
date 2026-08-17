package hoopinspect_test

import (
	"errors"
	"testing"

	"github.com/hoophq/hoopinspect"
	_ "github.com/hoophq/hoopinspect/codec/all"
)

func TestNewUnsupportedProtocol(t *testing.T) {
	_, err := hoopinspect.New("oracle")
	if !errors.Is(err, hoopinspect.ErrUnsupportedProtocol) {
		t.Errorf("err = %v, want ErrUnsupportedProtocol", err)
	}
}

func TestRegisteredCoversAllShippedProtocols(t *testing.T) {
	got := map[hoopinspect.Protocol]bool{}
	for _, p := range hoopinspect.Registered() {
		got[p] = true
	}
	for _, want := range []hoopinspect.Protocol{
		hoopinspect.Postgres, hoopinspect.HTTP,
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
	for _, p := range hoopinspect.Registered() {
		if _, err := hoopinspect.New(p); err != nil {
			t.Errorf("New(%q) failed though the protocol is registered: %v", p, err)
		}
	}
}

func TestNewReturnsDistinctCodecs(t *testing.T) {
	// Codecs must not be shared across connections: a stateful one would let
	// two connections corrupt each other's reassembly buffer. The codec
	// packages assert the behavioral version; this asserts the registry
	// contract that makes it possible.
	a, err := hoopinspect.New(hoopinspect.Postgres)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := hoopinspect.New(hoopinspect.Postgres)
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

func (s *stubCodec) Protocol() hoopinspect.Protocol { return "stub" }

func (s *stubCodec) Decode(dir hoopinspect.Direction, data []byte) ([]hoopinspect.Statement, int, error) {
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
	return []hoopinspect.Statement{{
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
	insp := hoopinspect.NewWithCodec(c)

	if _, err := insp.Inspect(hoopinspect.FromClient, []byte("abcdef")); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Buffered() != 2 {
		t.Errorf("Buffered = %d, want 2", insp.Buffered())
	}

	if _, err := insp.Inspect(hoopinspect.FromClient, []byte("gh")); err != nil {
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
	insp := hoopinspect.NewWithCodec(c)

	if _, err := insp.Inspect(hoopinspect.FromClient, []byte("xyz")); !errors.Is(err, boom) {
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
	insp := hoopinspect.NewWithCodec(c)
	insp.SetMaxBuffer(64)

	var err error
	for range 10 {
		_, err = insp.Inspect(hoopinspect.FromClient, make([]byte, 32))
		if err != nil {
			break
		}
	}
	if !errors.Is(err, hoopinspect.ErrBufferOverflow) {
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
	insp := hoopinspect.NewWithCodec(c)
	insp.SetMaxBuffer(64)

	if _, err := insp.Inspect(hoopinspect.FromClient, make([]byte, 4096)); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Buffered() != 0 {
		t.Errorf("Buffered = %d, want 0", insp.Buffered())
	}
}

func TestSetMaxBufferZeroRestoresDefault(t *testing.T) {
	c := &stubCodec{consume: func(d []byte) (int, error) { return 0, nil }}
	insp := hoopinspect.NewWithCodec(c)
	insp.SetMaxBuffer(16)
	insp.SetMaxBuffer(0)

	// 1 KiB is far above the 16-byte cap but far below the default, so it
	// only succeeds if the default was restored.
	if _, err := insp.Inspect(hoopinspect.FromClient, make([]byte, 1024)); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
}

func TestReset(t *testing.T) {
	c := &stubCodec{consume: func(d []byte) (int, error) { return 0, nil }}
	insp := hoopinspect.NewWithCodec(c)

	insp.Inspect(hoopinspect.FromClient, []byte("partial"))
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
	insp := hoopinspect.NewWithCodec(c)

	stmts, err := insp.Inspect(hoopinspect.FromClient, nil)
	if err != nil || len(stmts) != 0 {
		t.Errorf("Inspect(nil) = (%v, %v), want (nil, nil)", stmts, err)
	}
	if len(c.seen) != 0 {
		t.Error("codec was called with no data")
	}
}

func TestProtocolAccessor(t *testing.T) {
	insp, err := hoopinspect.New(hoopinspect.Postgres)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if insp.Protocol() != hoopinspect.Postgres {
		t.Errorf("Protocol = %q, want postgres", insp.Protocol())
	}
}

func TestRegisterRejectsNilFactory(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Register(nil) did not panic")
		}
	}()
	hoopinspect.Register(nil)
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register did not panic")
		}
	}()
	// Postgres is already registered via codec/all.
	hoopinspect.Register(func() hoopinspect.Codec { return &stubPostgres{} })
}

type stubPostgres struct{}

func (stubPostgres) Protocol() hoopinspect.Protocol { return hoopinspect.Postgres }
func (stubPostgres) Decode(hoopinspect.Direction, []byte) ([]hoopinspect.Statement, int, error) {
	return nil, 0, nil
}

// --- classifier ----------------------------------------------------------

func TestClassifySQL(t *testing.T) {
	tests := []struct {
		sql     string
		wantOp  hoopinspect.Operation
		wantTbl []string
	}{
		{"SELECT * FROM t", hoopinspect.OpSelect, []string{"t"}},
		{"select * from T", hoopinspect.OpSelect, []string{"t"}},
		{"INSERT INTO a SELECT * FROM b", hoopinspect.OpInsert, []string{"a", "b"}},
		{"UPDATE a SET x = 1 FROM b", hoopinspect.OpUpdate, []string{"a", "b"}},
		{"BEGIN", hoopinspect.OpBegin, nil},
		{"COMMIT", hoopinspect.OpCommit, nil},
		{"ROLLBACK", hoopinspect.OpRollback, nil},
		{"START TRANSACTION", hoopinspect.OpBegin, nil},
		{"SHOW TABLES", hoopinspect.OpShow, nil},
		// GRANT rewrites the ACL of the named relation, so the relation
		// is reported now. The old lexer had no introducer for ON and
		// found nothing here.
		{"GRANT SELECT ON t TO alice", hoopinspect.OpGrant, []string{"t"}},
		{"", hoopinspect.OpUnknown, nil},
		{"   ", hoopinspect.OpUnknown, nil},
		{"not sql at all", hoopinspect.OpUnknown, nil},
	}
	for _, tc := range tests {
		t.Run(tc.sql, func(t *testing.T) {
			op, tables := hoopinspect.ClassifySQL(tc.sql)
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
	_, tables := hoopinspect.ClassifySQL("SELECT * FROM t JOIN t AS t2 ON true")
	if len(tables) != 1 || tables[0] != "t" {
		t.Errorf("Tables = %v, want [t]", tables)
	}
}
