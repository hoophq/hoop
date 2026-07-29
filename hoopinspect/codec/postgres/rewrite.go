package postgres

import (
	"encoding/binary"

	"github.com/hoophq/hoopinspect"
)

// CellMasker rewrites one result-set cell.
//
// column is the name the server gave it and may be empty when the result set
// arrived without a description. value is the cell as it appears on the wire;
// returning it unchanged means "leave this alone", and the rewriter then skips
// re-encoding that row entirely.
//
// It is called once per cell of every buffered row, so it is on the hot path
// of every SELECT that returns data.
//
// Declared as an alias rather than a defined type so this codec satisfies
// gate.Reframer structurally: a defined func type would not match an
// interface written with the bare signature, and the gate must not import a
// codec to describe a capability.
type CellMasker = func(column string, value []byte) []byte

// maxHeldBytes bounds the bytes a rewriter will hold back while waiting for a
// result set to end.
//
// Buffering is what makes re-framing possible — a row already forwarded cannot
// be masked — but an unbounded buffer turns a large SELECT into a memory
// exhaustion. Past this the rewriter flushes what it holds MASKED and keeps
// going: the client gets correct, complete data, and only the batching
// granularity changes.
const maxHeldBytes = 4 << 20

// Rewrite masks a response stream in place, re-framing every row it changes.
//
// # Why this cannot be byte substitution
//
// A DataRow length-prefixes the message and every column inside it:
//
//	'D' int32(len) int16(cols) [ int32(len) bytes ]...
//
// Replacing ada@example.com (15 bytes) with [REDACTED:EMAIL_ADDRESS] (24)
// leaves both prefixes describing the old size. The client reads the declared
// number of bytes, lands mid-value, and reports "lost synchronization with
// server". So a changed row is not patched — it is rebuilt, with every length
// recomputed from the masked values.
//
// # Why it buffers
//
// A row cannot be rebuilt after it has been forwarded, and a row is not
// complete until its last column has arrived. Rewrite therefore returns only
// the bytes it is done with: complete messages that either need no change or
// have already been re-framed. Whatever it holds is emitted on the next call
// that completes the result set, or by Flush.
//
// Messages other than DataRow pass through unchanged and IN ORDER, which is
// the property that keeps the client's state machine intact.
func (c *Codec) Rewrite(data []byte, mask CellMasker) ([]byte, hoopinspect.ReframeResult, error) {
	if mask == nil {
		return data, hoopinspect.ReframeResult{}, nil
	}

	// Prepend anything held from an earlier call: a message split across two
	// reads is only decodable once its halves are adjacent.
	if len(c.pending) > 0 {
		c.pending = append(c.pending, data...)
		data = c.pending
		c.pending = nil
	}

	var (
		out  []byte
		res  hoopinspect.ReframeResult
		pos  int
		emit = func(b []byte) { out = append(out, b...) }
	)

	for pos < len(data) {
		if len(data)-pos < 5 {
			break
		}
		tag := data[pos]
		msgLen := binary.BigEndian.Uint32(data[pos+1 : pos+5])
		if msgLen < 4 || msgLen > maxMessageLen {
			return out, res, ErrMalformed
		}
		total := 1 + int(msgLen)
		if len(data)-pos < total {
			break // partial message
		}
		msg := data[pos : pos+total]
		payload := data[pos+5 : pos+total]

		switch tag {
		case tagRowDescription:
			// Flush any rows still held: they belong to the previous result
			// set and are described by the previous layout.
			emit(c.flushHeld(mask, &res))
			cols, ok := decodeRowDescription(payload)
			if !ok {
				return out, res, ErrMalformed
			}
			c.maskCols = columnNames(cols)
			emit(msg)

		case tagDataRow:
			c.held = append(c.held, msg...)
			if len(c.held) >= maxHeldBytes {
				emit(c.flushHeld(mask, &res))
			}

		default:
			// Any other message ends the run of rows before it. Emitting the
			// held rows first is what preserves order.
			emit(c.flushHeld(mask, &res))
			if tag == tagReadyForQuery {
				c.maskCols = nil
			}
			emit(msg)
		}

		pos += total
	}

	// Retain the incomplete tail for the next call.
	if pos < len(data) {
		c.pending = append(c.pending[:0], data[pos:]...)
	}
	return out, res, nil
}

// Flush emits every row still held, masked.
//
// The caller must call it when the connection closes: rows buffered when the
// peer went away would otherwise be dropped, which silently truncates the
// client's result set — a far worse failure than masking late.
func (c *Codec) Flush(mask CellMasker) []byte {
	if mask == nil {
		out := append(c.pending, c.held...)
		c.pending, c.held = nil, nil
		return out
	}
	var res hoopinspect.ReframeResult
	out := c.flushHeld(mask, &res)
	// A partial message can never be re-framed, but dropping it would
	// desynchronize the client. Forward it as-is and let the peer decide.
	out = append(out, c.pending...)
	c.pending = nil
	return out
}

// flushHeld masks and re-encodes the buffered rows, returning them in order.
func (c *Codec) flushHeld(mask CellMasker, res *hoopinspect.ReframeResult) []byte {
	if len(c.held) == 0 {
		return nil
	}
	held := c.held
	c.held = nil

	var out []byte
	pos := 0
	for pos < len(held) {
		if len(held)-pos < 5 {
			// Cannot happen: only complete messages are appended. Forward the
			// remainder rather than dropping bytes.
			out = append(out, held[pos:]...)
			break
		}
		total := 1 + int(binary.BigEndian.Uint32(held[pos+1:pos+5]))
		if total < 5 || pos+total > len(held) {
			out = append(out, held[pos:]...)
			break
		}
		msg := held[pos : pos+total]
		rewritten, changed := c.maskRow(msg[5:], mask, res)
		if changed {
			out = append(out, rewritten...)
			res.Rows++
		} else {
			out = append(out, msg...)
		}
		pos += total
	}
	return out
}

// maskRow applies the masker to every cell of one DataRow payload, returning a
// freshly encoded message when anything changed.
//
// The unchanged case returns changed=false and allocates nothing, because the
// overwhelming majority of rows in any result set contain no sensitive value.
func (c *Codec) maskRow(payload []byte, mask CellMasker, res *hoopinspect.ReframeResult) ([]byte, bool) {
	if len(payload) < 2 {
		return nil, false
	}
	count := int(binary.BigEndian.Uint16(payload[:2]))

	type cell struct {
		val  []byte
		null bool
	}
	cells := make([]cell, 0, count)

	pos := 2
	changed := 0
	for i := 0; i < count; i++ {
		if pos+4 > len(payload) {
			return nil, false // malformed; leave it alone
		}
		n := binary.BigEndian.Uint32(payload[pos : pos+4])
		pos += 4

		if n == nullColumn {
			cells = append(cells, cell{null: true})
			continue
		}
		if pos+int(n) > len(payload) {
			return nil, false
		}
		val := payload[pos : pos+int(n)]
		pos += int(n)

		col := ""
		if i < len(c.maskCols) {
			col = c.maskCols[i]
		}
		masked := mask(col, val)
		if masked == nil {
			// A masker must not delete a cell; treat nil as "unchanged".
			masked = val
		}
		if !equalBytes(masked, val) {
			changed++
		}
		cells = append(cells, cell{val: masked})
	}

	if changed == 0 {
		return nil, false
	}
	res.Cells += changed

	// Rebuild: message length and every column prefix recomputed.
	size := 4 + 2
	for _, cl := range cells {
		size += 4
		if !cl.null {
			size += len(cl.val)
		}
	}
	out := make([]byte, 0, 1+size)
	out = append(out, tagDataRow)
	out = binary.BigEndian.AppendUint32(out, uint32(size))
	out = binary.BigEndian.AppendUint16(out, uint16(len(cells)))
	for _, cl := range cells {
		if cl.null {
			out = binary.BigEndian.AppendUint32(out, nullColumn)
			continue
		}
		out = binary.BigEndian.AppendUint32(out, uint32(len(cl.val)))
		out = append(out, cl.val...)
	}
	return out, true
}

// columnNames flattens a description to the names the masker sees.
func columnNames(cols []hoopinspect.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// equalBytes avoids importing bytes for one comparison on the hot path.
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
