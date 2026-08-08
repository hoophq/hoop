package mssql

import (
	"encoding/binary"

	"github.com/hoophq/hoopinspect"
)

// CellMasker rewrites one result-set cell.
//
// column is the name the server gave it; value is the cell decoded to UTF-8.
// Returning it unchanged means "leave this alone", and the rewriter then keeps
// the original bytes rather than re-encoding them.
//
// Declared as an alias rather than a defined type so this codec satisfies
// gate.Reframer structurally, matching codec/postgres.
type CellMasker = func(column string, value []byte) []byte

// maxHeldBytes bounds what the rewriter buffers while waiting for a token to
// complete. Past this it gives up rewriting and forwards what it holds, so a
// bulk result set cannot turn masking into memory exhaustion.
const maxHeldBytes = 4 << 20

// defaultPacketSize is the TDS packet size to emit when re-packetizing. 4096
// is the protocol default and every client accepts it; a server that
// negotiated something larger simply gets more, smaller packets back, which is
// legal and invisible above the transport.
const defaultPacketSize = 4096

// Rewrite masks a response stream, re-framing every row it changes.
//
// # Why this is harder than Postgres
//
// A pgwire DataRow is one self-contained message with an int32 length, so
// rewriting it means recomputing two numbers. TDS has two nested framings: an
// 8-byte packet header wrapping a token stream, where one ROW token can span
// packets and one packet can hold many tokens. Changing a value changes the
// token's length, which changes how the tokens repack.
//
// So this strips the packet headers, rewrites the token stream, and lays fresh
// packets over the result. Byte-patching in place cannot work: a longer value
// has nowhere to go.
//
// # When it declines
//
// A column type whose length this decoder cannot compute (SQL_VARIANT, XML,
// UDT) disables rewriting for the rest of the connection. Guessing a length
// desynchronizes the client, which is worse than an unmasked value: the user
// sees a protocol error instead of data, and the operator sees a bug rather
// than a gap. Statements and policy are unaffected; only masking stops.
func (c *Codec) Rewrite(data []byte, mask CellMasker) ([]byte, hoopinspect.ReframeResult, error) {
	var res hoopinspect.ReframeResult
	if mask == nil || c.noRewrite {
		return data, res, nil
	}

	c.held = append(c.held, data...)
	if len(c.held)+len(c.tokenBuf) > maxHeldBytes {
		out := append(append([]byte(nil), c.rawBuf...), c.held...)
		c.held, c.tokenBuf, c.rawBuf = nil, nil, nil
		c.noRewrite = true
		return out, res, nil
	}

	// Two framings, peeled in order. Whole packets first, because a partial
	// one has no reliable length yet.
	stream, consumed, eom := drainPackets(c.held)
	raw := append([]byte(nil), c.held[:consumed]...)
	c.held = append([]byte(nil), c.held[consumed:]...)
	c.tokenBuf = append(c.tokenBuf, stream...)
	c.rawBuf = append(c.rawBuf, raw...)
	if len(c.tokenBuf) == 0 {
		return nil, res, nil
	}

	// Then whole TOKENS, which is a different boundary: one ROW routinely
	// spans packets, and rewriting half of it corrupts everything after.
	rewritten, used, r, st := c.rewriteTokens(c.tokenBuf, mask)
	res = r

	// Whether an incomplete token may be HELD depends on which phase this is.
	//
	// Inside a result set, holding is required: one ROW routinely spans
	// packets and half a row cannot be rewritten.
	//
	// During login it is fatal. The server's reply carries tokens this codec
	// does not model, and mistaking "I cannot measure this" for "more is
	// coming" makes the relay sit on a complete login response waiting for
	// bytes that already arrived. The client then times out with "Unable to
	// complete login process due to delay in login response". So before the
	// first COLMETADATA the rewriter forwards and never holds.
	if st != parseOK && !c.seenColMeta {
		out := append(append([]byte(nil), c.rawBuf...), c.held...)
		c.held, c.tokenBuf, c.rawBuf = nil, nil, nil
		return out, res, nil
	}

	if st == parseCannot {
		// Something unmeasurable mid-result-set. Keep the prefix that was
		// rewritten and pass the rest through untouched.
		//
		// Emitting the ORIGINAL bytes for the whole batch would be worse than
		// wasteful: values already masked would go out in the clear while
		// res.Cells still claimed they had been masked, so the audit trail
		// would record masking that never reached the client.
		out := append(packetize(rewritten, false), packetize(c.tokenBuf[used:], eom)...)
		out = append(out, c.held...)
		c.held, c.tokenBuf, c.rawBuf = nil, nil, nil
		c.noRewrite = true
		return out, res, nil
	}

	c.tokenBuf = append([]byte(nil), c.tokenBuf[used:]...)
	c.rawBuf = nil
	// EOM only once nothing is held back, or the client would see a message
	// end with rows still to come.
	return packetize(rewritten, eom && len(c.tokenBuf) == 0 && len(c.held) == 0), res, nil
}

// Flush returns whatever the rewriter is still holding.
//
// A connection can close mid-token, and bytes held for a row that never
// completed still belong to the client. Dropping them looks like a truncated
// result rather than a masking bug, which is the harder failure to diagnose.
func (c *Codec) Flush(mask CellMasker) []byte {
	out := append(packetize(c.tokenBuf, true), c.held...)
	c.held, c.tokenBuf, c.rawBuf = nil, nil, nil
	return out
}

// drainPackets strips the 8-byte headers from every COMPLETE packet in buf,
// returning the concatenated token stream, how many bytes of buf it used, and
// whether the last packet closed a message.
func drainPackets(buf []byte) (stream []byte, consumed int, eom bool) {
	pos := 0
	for {
		if len(buf)-pos < headerLen {
			return stream, pos, eom
		}
		length := int(binary.BigEndian.Uint16(buf[pos+2 : pos+4]))
		if length < headerLen || len(buf)-pos < length {
			return stream, pos, eom
		}
		stream = append(stream, buf[pos+headerLen:pos+length]...)
		eom = buf[pos+1]&statusEOM != 0
		pos += length
	}
}

// packetize lays TDS reply packets over a token stream.
func packetize(stream []byte, eom bool) []byte {
	if len(stream) == 0 {
		return nil
	}
	const payload = defaultPacketSize - headerLen
	out := make([]byte, 0, len(stream)+((len(stream)/payload)+1)*headerLen)

	for i, id := 0, byte(1); i < len(stream); id++ {
		end := min(i+payload, len(stream))
		last := end == len(stream)

		var hdr [headerLen]byte
		hdr[0] = pktReply
		if last && eom {
			hdr[1] = statusEOM
		}
		binary.BigEndian.PutUint16(hdr[2:4], uint16(headerLen+end-i))
		hdr[6] = id
		out = append(out, hdr[:]...)
		out = append(out, stream[i:end]...)
		i = end
	}
	return out
}

// rewriteTokens walks the token stream, masking ROW and NBCROW values.
//
// ok=false means a token could not be measured, and the caller forwards the
// original bytes rather than an emission built on a bad parse.
func (c *Codec) rewriteTokens(
	stream []byte, mask CellMasker,
) (out []byte, used int, res hoopinspect.ReframeResult, st parseStatus) {
	out = make([]byte, 0, len(stream))
	p := 0

	for p < len(stream) {
		tok := stream[p]

		switch tok {
		case tokColMetaData:
			cols, n, st := parseColMetaData(stream[p+1:])
			if st != parseOK {
				return out, p, res, st
			}
			c.cols = cols
			c.seenColMeta = true
			out = append(out, stream[p:p+1+n]...)
			p += 1 + n

		case tokRow, tokNBCRow:
			if c.cols == nil {
				return out, p, res, parseCannot
			}
			row, n, changed, cells, st := c.rewriteRow(stream[p:], mask, tok == tokNBCRow)
			if st != parseOK {
				return out, p, res, st
			}
			out = append(out, row...)
			p += n
			if changed {
				res.Rows++
				res.Cells += cells
			}

		default:
			n, st := skipToken(stream[p:])
			if st != parseOK {
				return out, p, res, st
			}
			out = append(out, stream[p:p+n]...)
			p += n
		}
	}
	return out, p, res, parseOK
}

// rewriteRow masks one ROW or NBCROW token and returns its replacement.
func (c *Codec) rewriteRow(
	b []byte, mask CellMasker, nbc bool,
) (out []byte, n int, changed bool, cells int, st parseStatus) {
	p := 1 // the token byte

	// NBCROW prefixes a null bitmap, one bit per column, LSB first.
	var nulls []byte
	if nbc {
		nb := (len(c.cols) + 7) / 8
		if len(b)-p < nb {
			return nil, 0, false, 0, parseNeedMore
		}
		nulls = b[p : p+nb]
		p += nb
	}

	out = append(out, b[:p]...)

	for i, col := range c.cols {
		if nbc && nulls[i/8]&(1<<(i%8)) != 0 {
			continue // the bitmap says NULL; no bytes on the wire
		}

		_, val, adv, isNull, cst := readCell(b[p:], col)
		if cst != parseOK {
			return nil, 0, false, 0, cst
		}

		if isNull || !col.text || len(val) == 0 {
			out = append(out, b[p:p+adv]...)
			p += adv
			continue
		}

		masked := mask(col.name, val)
		if string(masked) == string(val) {
			out = append(out, b[p:p+adv]...)
			p += adv
			continue
		}

		enc, fits := encodeCell(masked, col)
		if !fits {
			// The masked value does not fit this column's encoding. Keep the
			// original: a value that leaks is recoverable, a frame that lies
			// about its length is not.
			out = append(out, b[p:p+adv]...)
			p += adv
			continue
		}
		out = append(out, enc...)
		p += adv
		changed = true
		cells++
	}
	return out, p, changed, cells, parseOK
}
