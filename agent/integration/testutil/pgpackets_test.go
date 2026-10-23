//go:build integration

package testutil

import (
	"bytes"
	"testing"
)

func TestParsePGMessages(t *testing.T) {
	ready := []byte{'Z', 0, 0, 0, 5, 'I'}
	empty := func(typ byte) []byte { return []byte{typ, 0, 0, 0, 4} }

	t.Run("keeps messages with an empty body, also at the end", func(t *testing.T) {
		for _, tt := range []struct {
			payload []byte
			want    string
		}{
			{empty('1'), "1"},
			{bytes.Join([][]byte{empty('1'), empty('2'), ready}, nil), "12Z"},
			{bytes.Join([][]byte{ready, empty('n')}, nil), "Zn"},
		} {
			var types []byte
			for _, msg := range ParsePGMessages(tt.payload) {
				types = append(types, msg.Type)
			}
			if string(types) != tt.want {
				t.Errorf("got types %q, expected %q", types, tt.want)
			}
		}
	})

	t.Run("stops at a truncated message", func(t *testing.T) {
		msgs := ParsePGMessages(append(empty('1'), 'C', 0, 0, 0, 20, 'S'))
		if len(msgs) != 1 || msgs[0].Type != '1' {
			t.Fatalf("expected only the complete message, got %+v", msgs)
		}
	})
}

func TestPGExtendedBatch(t *testing.T) {
	query := "select *\nfrom \"Users\"\nLIMIT 3"
	msgs := PGExtendedBatch(query, "SELECT 1")
	var types []byte
	for _, raw := range msgs {
		parsed := ParsePGMessages(raw)
		if len(parsed) != 1 {
			t.Fatalf("each write must hold exactly one message, got %d in %q", len(parsed), raw)
		}
		types = append(types, parsed[0].Type)
	}
	if got, want := string(types), "PBDEPBDES"; got != want {
		t.Fatalf("got messages %q, expected %q", got, want)
	}
	parse := ParsePGMessages(msgs[0])[0].Payload
	if got := string(bytes.SplitN(parse[1:], []byte{0}, 2)[0]); got != query {
		t.Fatalf("Parse must carry the query as written, got %q", got)
	}
	if one := PGExtendedQuery("SELECT 1"); len(one) != 5 {
		t.Fatalf("one query must be one cycle (P B D E S), got %d messages", len(one))
	}
}
