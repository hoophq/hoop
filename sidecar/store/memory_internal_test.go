package store

import (
	"context"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// TestOwnDetachesEveryString fills each string, slice and map an event can
// reference, one at a time, with a slice of a larger buffer, as a lexer's
// table name is a slice of the query. own must copy it out, leave the
// caller's event alone, and count it. A field added to audit.Event or
// HTTPDetail without a line in ownStrings fails here instead of pinning
// memory the budget never sees.
func TestOwnDetachesEveryString(t *testing.T) {
	const big = 4096
	source := strings.Repeat("x", 1<<20)
	payload := source[100 : 100+big]
	inSource := func(s string) bool {
		p, b := uintptr(unsafe.Pointer(unsafe.StringData(s))), uintptr(unsafe.Pointer(unsafe.StringData(source)))
		return p >= b && p < b+uintptr(len(source))
	}

	var walk func(typ reflect.Type, path []int, name string)
	walk = func(typ reflect.Type, path []int, name string) {
		for i := range typ.NumField() {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			p := append(slices.Clone(path), i)
			label := name + "." + f.Name
			switch f.Type.Kind() {
			case reflect.String, reflect.Slice, reflect.Map:
				orig := buildEvent(p, fillValue(t, f.Type, payload, label))
				owned, content := own(orig)
				if content < big {
					t.Errorf("own counts %d content bytes for a %d-byte %s", content, big, label)
				}
				for _, s := range stringsAt(owned, p) {
					if inSource(s) {
						t.Errorf("own kept %s pointing into the caller's buffer", label)
					}
				}
				for _, s := range stringsAt(orig, p) {
					if !inSource(s) {
						t.Errorf("own rewrote the caller's %s", label)
					}
				}
				base, baseContent := own(buildEvent(p, reflect.Value{}))
				if got := eventSize(&owned, content) - eventSize(&base, baseContent); got < big {
					t.Errorf("eventSize counts %d bytes for a %d-byte %s", got, big, label)
				}
			case reflect.Pointer:
				if f.Type.Elem().Kind() == reflect.Struct {
					walk(f.Type.Elem(), p, label)
				}
			}
		}
	}
	walk(reflect.TypeFor[audit.Event](), nil, "Event")
}

// stringsAt returns every string the field at path holds.
func stringsAt(ev audit.Event, path []int) []string {
	cur := reflect.ValueOf(&ev).Elem()
	for _, i := range path {
		cur = reflect.Indirect(cur).Field(i)
	}
	var out []string
	var collect func(v reflect.Value)
	collect = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.String:
			out = append(out, v.String())
		case reflect.Slice:
			for i := range v.Len() {
				collect(v.Index(i))
			}
		case reflect.Map:
			for it := v.MapRange(); it.Next(); {
				collect(it.Key())
				collect(it.Value())
			}
		}
	}
	collect(cur)
	return out
}

// buildEvent returns an Event with every pointer on path allocated and the
// leaf at its end set to v, or left zero when v is invalid.
func buildEvent(path []int, v reflect.Value) audit.Event {
	var ev audit.Event
	cur := reflect.ValueOf(&ev).Elem()
	for depth, i := range path {
		fld := cur.Field(i)
		if depth == len(path)-1 {
			if v.IsValid() {
				fld.Set(v)
			}
			break
		}
		fld.Set(reflect.New(fld.Type().Elem()))
		cur = fld.Elem()
	}
	return ev
}

func fillValue(t *testing.T, typ reflect.Type, payload, label string) reflect.Value {
	t.Helper()
	switch typ.Kind() {
	case reflect.String:
		return reflect.ValueOf(payload).Convert(typ)
	case reflect.Slice:
		s := reflect.MakeSlice(typ, 1, 1)
		s.Index(0).Set(fillValue(t, typ.Elem(), payload, label))
		return s
	case reflect.Map:
		m := reflect.MakeMap(typ)
		m.SetMapIndex(fillValue(t, typ.Key(), payload[:16], label), fillValue(t, typ.Elem(), payload, label))
		return m
	}
	t.Fatalf("%s is a %s: teach this test and ownStrings how to copy it", label, typ)
	return reflect.Value{}
}

// heapInUse returns the live heap after a full collection.
func heapInUse() int64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}

func poolStatement(session session.ID, i int) audit.Event {
	return audit.Event{
		Kind: audit.KindStatement, Timestamp: time.Now().UTC(), SessionID: session,
		Principal: "pool@example.com", Protocol: inspect.Postgres, Connection: "appdb",
		Operation: inspect.OpSelect, Allowed: true,
		Statement: "SELECT * FROM orders WHERE id = " + strconv.Itoa(i) + " -- " + strings.Repeat("x", 1000),
		Metadata:  map[string]string{"pg.message": "Q"},
	}
}

// ENG-623: sixteen sessions that never close, as a connection pool keeps,
// must not grow the store past its byte budget. Before the budget, the store
// kept every event of a live session, and this load held ~200 MiB.
func TestLongLivedSessionsStayWithinTheByteBudget(t *testing.T) {
	const budget = 8 << 20
	before := heapInUse()

	m := NewMemoryStore(MemoryOptions{MaxSessions: 5000, MaxBytes: budget})
	ids := make([]session.ID, 16)
	for i := range ids {
		ids[i] = session.ID("pool-" + strconv.Itoa(i))
	}
	const events = 200_000 // ~200 MiB of statements
	for i := range events {
		if err := m.Write(context.Background(), poolStatement(ids[i%len(ids)], i)); err != nil {
			t.Fatal(err)
		}
	}

	if m.bytes > budget {
		t.Errorf("the store holds %d bytes for a %d-byte budget", m.bytes, budget)
	}
	if grew := heapInUse() - before; grew > 3*budget {
		t.Errorf("the heap grew %d MiB for an %d MiB budget", grew>>20, budget>>20)
	}

	var dropped int64
	for _, id := range ids {
		rec, err := m.Session(context.Background(), id)
		if err != nil {
			t.Fatalf("an open session was evicted: %v", err)
		}
		if rec.StatementCount != events/len(ids) {
			t.Errorf("%s counts %d statements, want %d: dropping events must not change the counters",
				id, rec.StatementCount, events/len(ids))
		}
		dropped += rec.EventsDropped
	}
	retained, _ := m.Events(context.Background(), EventFilter{})
	if dropped+retained.Total != events {
		t.Errorf("dropped %d + retained %d != %d written", dropped, retained.Total, events)
	}
	runtime.KeepAlive(m)
}

// A lexer's table name is a slice of the query it came from. The audit
// policy cuts the statement to 8 KiB, but a table name holding the original
// would keep the whole query alive while the budget counts six bytes.
func TestATableNameDoesNotPinItsQuery(t *testing.T) {
	const budget = 8 << 20
	before := heapInUse()

	for _, opts := range []audit.SinkOptions{{}, {RedactStatements: true}} {
		m := NewMemoryStore(MemoryOptions{MaxBytes: budget, Sink: opts})
		for i := range 200 { // 200 MiB of queries
			query := "SELECT * FROM orders WHERE note = '" + strings.Repeat("x", 1<<20) + strconv.Itoa(i) + "'"
			_ = m.Write(context.Background(), audit.Event{
				Kind: audit.KindStatement, SessionID: "pool", Operation: inspect.OpSelect,
				Statement: query, Tables: []string{query[14:20]},
			})
		}
		if grew := heapInUse() - before; grew > 3*budget {
			t.Errorf("redact=%t: the heap grew %d MiB for an %d MiB budget", opts.RedactStatements, grew>>20, budget>>20)
		}
		runtime.KeepAlive(m)
	}
}

// Evicting sessions must return their memory, not keep the capacity of a
// slice that once held them all.
func TestEvictedSessionsReleaseTheirMemory(t *testing.T) {
	before := heapInUse()

	m := NewMemoryStore(MemoryOptions{MaxSessions: 16, MaxBytes: 1 << 30})
	for i := range 100_000 {
		_ = m.Write(context.Background(), poolStatement(session.ID("big-"+strconv.Itoa(i%16)), i))
	}
	if held := heapInUse() - before; held < 50<<20 {
		t.Fatalf("the load held only %d MiB; the test proves nothing", held>>20)
	}

	// Sixteen new one-event sessions evict every big one.
	for i := range 16 {
		_ = m.Write(context.Background(), audit.Event{Kind: audit.KindSessionStart, SessionID: session.ID("new-" + strconv.Itoa(i))})
	}
	if held := heapInUse() - before; held > 4<<20 {
		t.Errorf("%d MiB stay allocated after every session holding them was evicted", held>>20)
	}
	runtime.KeepAlive(m)
}
