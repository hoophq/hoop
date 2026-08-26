package mssql

import (
	"encoding/binary"
	"unicode/utf16"
)

// plpNull and plpUnknownLen are the sentinels a PLP value uses in place of a
// real total length.
const (
	plpNull       uint64 = 0xFFFFFFFFFFFFFFFF
	plpUnknownLen uint64 = 0xFFFFFFFFFFFFFFFE
	ushortNull    uint16 = 0xFFFF
)

// readCell reads one column value from the front of b.
//
// It returns the raw on-wire bytes, the value decoded to UTF-8 (empty for
// non-text columns, which are measured but never interpreted), how many bytes
// the value occupied, and whether it was NULL.
func readCell(b []byte, col column) (raw, val []byte, n int, isNull bool, st parseStatus) {
	switch col.kind {
	case lenFixed:
		if len(b) < col.width {
			return nil, nil, 0, false, parseNeedMore
		}
		return b[:col.width], nil, col.width, false, parseOK

	case lenByte:
		if len(b) < 1 {
			return nil, nil, 0, false, parseNeedMore
		}
		l := int(b[0])
		if len(b) < 1+l {
			return nil, nil, 0, false, parseNeedMore
		}
		if l == 0 {
			return b[:1], nil, 1, true, parseOK
		}
		return b[1 : 1+l], decodeText(b[1:1+l], col), 1 + l, false, parseOK

	case lenUShort:
		if len(b) < 2 {
			return nil, nil, 0, false, parseNeedMore
		}
		l := binary.LittleEndian.Uint16(b[0:2])
		if l == ushortNull {
			return b[:2], nil, 2, true, parseOK
		}
		if len(b) < 2+int(l) {
			return nil, nil, 0, false, parseNeedMore
		}
		return b[2 : 2+int(l)], decodeText(b[2:2+int(l)], col), 2 + int(l), false, parseOK

	case lenPLP:
		return readPLPCell(b, col)

	case lenLong:
		// TEXT/NTEXT/IMAGE: a textptr, then a timestamp, then the data.
		if len(b) < 1 {
			return nil, nil, 0, false, parseNeedMore
		}
		ptrLen := int(b[0])
		if ptrLen == 0 {
			return b[:1], nil, 1, true, parseOK // NULL
		}
		p := 1 + ptrLen + 8 // textptr + timestamp
		if len(b) < p+4 {
			return nil, nil, 0, false, parseNeedMore
		}
		l := int(binary.LittleEndian.Uint32(b[p : p+4]))
		p += 4
		if len(b) < p+l {
			return nil, nil, 0, false, parseNeedMore
		}
		return b[p : p+l], decodeText(b[p:p+l], col), p + l, false, parseOK
	}
	return nil, nil, 0, false, parseNeedMore
}

// readPLPCell reads a MAX-typed value: a total length, then chunks, ending
// with a zero-length chunk.
func readPLPCell(b []byte, col column) (raw, val []byte, n int, isNull bool, st parseStatus) {
	if len(b) < 8 {
		return nil, nil, 0, false, parseNeedMore
	}
	total := binary.LittleEndian.Uint64(b[0:8])
	if total == plpNull {
		return b[:8], nil, 8, true, parseOK
	}
	p := 8
	var body []byte
	for {
		if len(b)-p < 4 {
			return nil, nil, 0, false, parseNeedMore
		}
		chunk := int(binary.LittleEndian.Uint32(b[p : p+4]))
		p += 4
		if chunk == 0 {
			break // terminator
		}
		if len(b)-p < chunk {
			return nil, nil, 0, false, parseNeedMore
		}
		body = append(body, b[p:p+chunk]...)
		p += chunk
	}
	return body, decodeText(body, col), p, false, parseOK
}

// decodeText converts a text column's bytes to UTF-8. Non-text columns return
// nil: their bytes are measured so the walk stays aligned, never interpreted.
func decodeText(b []byte, col column) []byte {
	if !col.text {
		return nil
	}
	if col.ucs2 {
		return []byte(ucs2ToString(b))
	}
	// Single-byte columns carry a code page this decoder does not resolve.
	// Treating them as Latin-1-ish bytes is right for the ASCII that mask
	// rules match on and harmless for the rest.
	return b
}

// encodeCell renders a masked value back into the column's wire encoding.
//
// ok=false when the value cannot be expressed there, which the caller treats
// as "keep the original bytes". A frame whose declared length disagrees with
// its contents desynchronizes the client for the rest of the connection.
func encodeCell(val []byte, col column) ([]byte, bool) {
	body := val
	if col.ucs2 {
		body = toUCS2(val)
	}

	switch col.kind {
	case lenByte:
		if len(body) > 0xFE {
			return nil, false
		}
		return append([]byte{byte(len(body))}, body...), true

	case lenUShort:
		if len(body) >= int(ushortNull) {
			return nil, false
		}
		out := make([]byte, 2, 2+len(body))
		binary.LittleEndian.PutUint16(out[0:2], uint16(len(body)))
		return append(out, body...), true

	case lenPLP:
		// One chunk plus a terminator is a legal PLP body and the simplest
		// shape to emit; a reader cannot tell it from the original chunking.
		out := make([]byte, 8, 8+4+len(body)+4)
		binary.LittleEndian.PutUint64(out[0:8], uint64(len(body)))
		out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
		out = append(out, body...)
		out = binary.LittleEndian.AppendUint32(out, 0)
		return out, true

	case lenLong:
		// A 16-byte textptr and an 8-byte timestamp keep the shape the client
		// expects; neither is meaningful once the value is inline.
		out := make([]byte, 0, 1+16+8+4+len(body))
		out = append(out, 16)
		out = append(out, make([]byte, 16)...)
		out = append(out, make([]byte, 8)...)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
		return append(out, body...), true
	}
	return nil, false
}

// toUCS2 encodes UTF-8 as little-endian UCS-2.
func toUCS2(b []byte) []byte {
	runes := utf16.Encode([]rune(string(b)))
	out := make([]byte, 0, len(runes)*2)
	for _, r := range runes {
		out = binary.LittleEndian.AppendUint16(out, r)
	}
	return out
}

// skipToken measures a token this rewriter does not modify.
//
// TDS gives no generic length field, so each token's rule is explicit. An
// unrecognized token returns ok=false and the rewriter steps aside rather than
// guessing, because a wrong length here corrupts every byte after it.
func skipToken(b []byte) (n int, st parseStatus) {
	if len(b) < 1 {
		return 0, parseNeedMore
	}
	switch b[0] {
	// Fixed-length tokens.
	case tokDone, tokDoneProc, tokDoneInProc:
		// Status(2) CurCmd(2) RowCount(8) on TDS 7.2+.
		if len(b) < 13 {
			return 0, parseNeedMore
		}
		return 13, parseOK
	case tokReturnStatus:
		if len(b) < 5 {
			return 0, parseNeedMore
		}
		return 5, parseOK
	case tokRowStat:
		if len(b) < 5 {
			return 0, parseNeedMore
		}
		return 5, parseOK

	// FEATUREEXTACK is NOT USHORT-framed, unlike its neighbours: it is a run
	// of (FeatureId, ULONG len, data) triples ending at FeatureId 0xFF.
	// Measuring it as a USHORT length desynchronizes the login response, and
	// the client then waits for bytes that already went past.
	case tokFeatureExtAck:
		p := 1
		for {
			if len(b)-p < 1 {
				return 0, parseNeedMore
			}
			if b[p] == 0xFF {
				return p + 1, parseOK
			}
			if len(b)-p < 5 {
				return 0, parseNeedMore
			}
			l := int(binary.LittleEndian.Uint32(b[p+1 : p+5]))
			p += 5 + l
			if len(b) < p {
				return 0, parseNeedMore
			}
		}

	// USHORT-length tokens: the length counts the bytes after it.
	case tokError, tokInfo, tokEnvChange, tokLoginAck, tokSSPI, tokTabName, tokColInfo:
		if len(b) < 3 {
			return 0, parseNeedMore
		}
		l := int(binary.LittleEndian.Uint16(b[1:3]))
		if len(b) < 3+l {
			return 0, parseNeedMore
		}
		return 3 + l, parseOK

	// ULONG-length tokens. SESSIONSTATE closes most SQL Server 2016+ result
	// sets, so measuring it as anything else throws away every mask applied
	// earlier in the same response.
	case tokSessionState, tokFedAuthInfo:
		if len(b) < 5 {
			return 0, parseNeedMore
		}
		l := int(binary.LittleEndian.Uint32(b[1:5]))
		if len(b) < 5+l {
			return 0, parseNeedMore
		}
		return 5 + l, parseOK

	// ORDER: USHORT length, then that many bytes of column indexes.
	case tokOrder:
		if len(b) < 3 {
			return 0, parseNeedMore
		}
		l := int(binary.LittleEndian.Uint16(b[1:3]))
		if len(b) < 3+l {
			return 0, parseNeedMore
		}
		return 3 + l, parseOK
	}
	return 0, parseCannot
}
