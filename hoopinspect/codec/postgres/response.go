package postgres

import (
	"encoding/binary"

	"github.com/hoophq/hoopinspect"
)

// Backend (server → client) message tags this codec understands. Everything
// else is skipped by its length, the same way the frontend decoder does.
//
// Wire format reference:
// https://www.postgresql.org/docs/current/protocol-message-formats.html
const (
	tagRowDescription  = 'T'
	tagDataRow         = 'D'
	tagCommandComplete = 'C'
	tagReadyForQuery   = 'Z'
	tagErrorResponse   = 'E'
	tagEmptyQuery      = 'I'
)

// nullColumn is the length field of a SQL NULL: -1 as an int32. It is the one
// value that is not a byte count, and treating it as one reads 4 GiB of
// whatever follows.
const nullColumn = 0xFFFFFFFF

// maxDecodedRows bounds how many rows one Decode call turns into structured
// values.
//
// A SELECT over a million-row table arrives as a million DataRow messages, and
// materializing them all to answer "does this contain an SSN" would put the
// relay's memory at the mercy of the query. Past this count the codec keeps
// counting rows but stops decoding their columns, and marks the batch
// Truncated so a policy knows its view is partial.
const maxDecodedRows = 1000

// rowDescription is the column layout of the result set currently streaming.
//
// It is codec state, not per-call state: the 'T' message arrives once and
// every 'D' after it is described by it, which is exactly why the Codec is a
// pointer type and why Register hands out a factory rather than an instance.
type rowDescription struct {
	columns []hoopinspect.Column
}

// decodeResponse parses backend messages, returning one Statement per
// completed result set.
//
// "Completed" means a CommandComplete ('C'), an ErrorResponse ('E') or an
// EmptyQueryResponse ('I') — the messages that end a query's output. Rows seen
// before that point accumulate on the codec, so a result set split across
// several reads still yields exactly one Statement.
func (c *Codec) decodeResponse(data []byte) ([]hoopinspect.Statement, int, error) {
	var stmts []hoopinspect.Statement
	pos := 0

	for pos < len(data) {
		if len(data)-pos < 5 {
			return stmts, pos, nil // need tag + length
		}

		tag := data[pos]
		msgLen := binary.BigEndian.Uint32(data[pos+1 : pos+5])

		// Length counts itself (4) but not the tag, so 4 is the floor.
		if msgLen < 4 || msgLen > maxMessageLen {
			return stmts, pos, ErrMalformed
		}

		total := 1 + int(msgLen)
		if len(data)-pos < total {
			return stmts, pos, nil // partial message, retain it
		}

		payload := data[pos+5 : pos+total]

		switch tag {
		case tagRowDescription:
			cols, ok := decodeRowDescription(payload)
			if !ok {
				return stmts, pos, ErrMalformed
			}
			// A new description starts a new result set. Anything pending is
			// a result set whose terminator we never saw.
			c.resetResult()
			c.rowDesc = &rowDescription{columns: cols}

		case tagDataRow:
			if !c.countRow(payload) {
				return stmts, pos, ErrMalformed
			}

		case tagCommandComplete, tagErrorResponse, tagEmptyQuery:
			// End of one query's output. Emit whatever we accumulated, even
			// when it is zero rows: "this query returned nothing" is a fact
			// worth auditing.
			if stmt, ok := c.flushResult(cstring(payload), tag); ok {
				stmts = append(stmts, stmt)
			}

		case tagReadyForQuery:
			// The backend is idle again. Any partial state here belongs to a
			// result set that will never be completed.
			c.resetResult()
		}

		pos += total
	}

	return stmts, pos, nil
}

// countRow records one DataRow, decoding its columns while under the budget.
func (c *Codec) countRow(payload []byte) bool {
	if len(payload) < 2 {
		return false
	}
	c.rowCount++

	if c.rowCount > maxDecodedRows {
		c.truncated = true
		return true // still counted, just not decoded
	}
	// Validate the frame even when we keep no values: a malformed row means
	// the stream is desynchronized and every later offset is wrong.
	return validateDataRow(payload)
}

// validateDataRow walks a DataRow's column length prefixes and reports whether
// they exactly span the payload.
func validateDataRow(payload []byte) bool {
	count := int(binary.BigEndian.Uint16(payload[:2]))
	pos := 2
	for i := 0; i < count; i++ {
		if pos+4 > len(payload) {
			return false
		}
		n := binary.BigEndian.Uint32(payload[pos : pos+4])
		pos += 4
		if n == nullColumn {
			continue
		}
		if pos+int(n) > len(payload) {
			return false
		}
		pos += int(n)
	}
	return pos == len(payload)
}

// flushResult turns the accumulated rows into a Statement and clears the
// accumulator.
//
// Text is the command tag ("SELECT 2", "DELETE 1") for a CommandComplete,
// which is the server's own one-line summary of what it did. For an
// ErrorResponse it is the error's primary message, so a failed query is
// auditable as a failure rather than as silence.
func (c *Codec) flushResult(text string, tag byte) (hoopinspect.Statement, bool) {
	// Nothing at all seen: not a result set, just a bare acknowledgement of
	// something we did not track.
	if c.rowDesc == nil && c.rowCount == 0 && tag != tagErrorResponse {
		c.resetResult()
		return hoopinspect.Statement{}, false
	}

	detail := &hoopinspect.ResultDetail{
		RowCount:  c.rowCount,
		Truncated: c.truncated,
	}
	if c.rowDesc != nil {
		detail.Columns = c.rowDesc.columns
	}

	stmt := hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Direction: hoopinspect.FromServer,
		Text:      text,
		// A response is not a verb the client issued: the operation lives on
		// the REQUEST statement, which the audit trail already has. Adding an
		// Operation value for it would put a non-verb in a field every SQL
		// rule matches on.
		Operation: hoopinspect.OpUnknown,
		Result:    detail,
		Metadata:  map[string]string{"pg.message": terminatorName(tag)},
	}

	c.resetResult()
	return stmt, true
}

// terminatorName names the message that ended the result set, so a failed
// query is selectable in the audit trail rather than indistinguishable from a
// successful one that returned no rows.
func terminatorName(tag byte) string {
	switch tag {
	case tagErrorResponse:
		return "ErrorResponse"
	case tagEmptyQuery:
		return "EmptyQueryResponse"
	default:
		return "CommandComplete"
	}
}

// resetResult clears per-result-set accumulation. The row description is
// dropped with it: the next result set describes itself.
func (c *Codec) resetResult() {
	c.rowDesc = nil
	c.rowCount = 0
	c.truncated = false
}

// decodeRowDescription parses a 'T' payload into columns.
//
// Layout after the tag and length:
//
//	int16   field count
//	per field:
//	  string  name, NUL-terminated
//	  int32   table OID
//	  int16   column attribute number
//	  int32   data type OID
//	  int16   data type size
//	  int32   type modifier
//	  int16   format code (0 text, 1 binary)
func decodeRowDescription(payload []byte) ([]hoopinspect.Column, bool) {
	if len(payload) < 2 {
		return nil, false
	}
	count := int(binary.BigEndian.Uint16(payload[:2]))
	pos := 2

	// A field costs at least 1 (NUL) + 18 fixed bytes, so a count that could
	// not fit is garbage rather than a truncated read.
	if count < 0 || count*19 > len(payload) {
		return nil, false
	}

	cols := make([]hoopinspect.Column, 0, count)
	for i := 0; i < count; i++ {
		n := indexNUL(payload[pos:])
		if n < 0 {
			return nil, false
		}
		name := string(payload[pos : pos+n])
		pos += n + 1

		if pos+18 > len(payload) {
			return nil, false
		}
		cols = append(cols, hoopinspect.Column{
			Name:        name,
			DataTypeOID: binary.BigEndian.Uint32(payload[pos+6 : pos+10]),
		})
		pos += 18
	}
	return cols, true
}
