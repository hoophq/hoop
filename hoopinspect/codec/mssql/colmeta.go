package mssql

import "encoding/binary"

// TDS response tokens. Only the ones that bear on a result set are named; the
// rest are skipped by the length rules in skipToken.
const (
	tokColMetaData   = 0x81
	tokRow           = 0xd1
	tokNBCRow        = 0xd2
	tokDone          = 0xfd
	tokDoneProc      = 0xfe
	tokDoneInProc    = 0xff
	tokError         = 0xaa
	tokInfo          = 0xab
	tokEnvChange     = 0xe3
	tokLoginAck      = 0xad
	tokOrder         = 0xa9
	tokReturnStatus  = 0x79
	tokReturnValue   = 0xac
	tokFeatureExtAck = 0xae
	tokSSPI          = 0xed
	tokRowStat       = 0xd3
	tokSessionState  = 0xe4
	tokFedAuthInfo   = 0xee
	tokTabName       = 0xa4
	tokColInfo       = 0xa5
)

// TDS data types, grouped by how a ROW encodes their value.
//
// The grouping is the whole game for re-framing: a rewriter that cannot
// compute a value's length cannot find the next value, and a decoder that
// loses its place mid-row emits garbage the client reads as a protocol error.
const (
	tNull       = 0x1f
	tInt1       = 0x30
	tBit        = 0x32
	tInt2       = 0x34
	tInt4       = 0x38
	tDateTim4   = 0x3a
	tFlt4       = 0x3b
	tMoney      = 0x3c
	tDateTime   = 0x3d
	tFlt8       = 0x3e
	tMoney4     = 0x7a
	tInt8       = 0x7f
	tGuid       = 0x24
	tIntN       = 0x26
	tDecimal    = 0x37
	tNumeric    = 0x3f
	tBitN       = 0x68
	tDecimalN   = 0x6a
	tNumericN   = 0x6c
	tFltN       = 0x6d
	tMoneyN     = 0x6e
	tDateTimeN  = 0x6f
	tDateN      = 0x28
	tTimeN      = 0x29
	tDateTime2N = 0x2a
	tDTOffsetN  = 0x2b
	tChar       = 0x2f
	tVarChar    = 0x27
	tBinary     = 0x2d
	tVarBinary  = 0x25
	tBigVarBin  = 0xa5
	tBigVarChar = 0xa7
	tBigBinary  = 0xad
	tBigChar    = 0xaf
	tNVarChar   = 0xe7
	tNChar      = 0xef
	tText       = 0x23
	tImage      = 0x22
	tNText      = 0x63
	tVariant    = 0x62
	tXML        = 0xf1
)

// parseStatus separates "the bytes have not all arrived" from "this cannot be
// decoded at all". Collapsing the two is what makes a rewriter give up on a
// row that merely straddled a packet boundary.
type parseStatus int

const (
	parseOK parseStatus = iota
	parseNeedMore
	parseCannot
)

// lenKind says how a ROW prefixes this column's value.
type lenKind int

const (
	lenFixed   lenKind = iota // no prefix; width bytes inline
	lenByte                   // BYTE length, 0 means NULL
	lenUShort                 // USHORT length, 0xFFFF means NULL
	lenPLP                    // partially length-prefixed (MAX types)
	lenLong                   // textptr + timestamp + LONG length (TEXT/NTEXT/IMAGE)
	lenUnknown                // not decodable; disables rewriting for the result set
)

// column is one entry of a COLMETADATA token, reduced to what a rewriter needs.
type column struct {
	name  string
	kind  lenKind
	width int  // for lenFixed
	text  bool // carries character data a mask rule can act on
	ucs2  bool // NVARCHAR/NCHAR/NTEXT store UCS-2, everything else single-byte
}

// parseColMetaData reads a COLMETADATA token body starting at b[0], returning
// the columns and the number of bytes consumed.
//
// A nil cols with parseOK means the token declared count 0xFFFF, "no metadata
// change": the previous layout stays in force and the caller keeps whatever it
// already has. That is distinct from a real zero-column result, which returns
// an empty but non-nil slice.
//
// parseCannot means a type this decoder cannot measure appeared. The caller
// then stops rewriting rather than guessing a length: forwarding the result
// set unmasked loses a control, and mis-framing it corrupts the client's
// stream.
func parseColMetaData(b []byte) (cols []column, n int, st parseStatus) {
	if len(b) < 2 {
		return nil, 0, parseNeedMore
	}
	count := int(binary.LittleEndian.Uint16(b[0:2]))
	p := 2
	// 0xFFFF means "no metadata change"; the previous layout still applies.
	//
	// Treating this as unmeasurable would latch noRewrite and drop masking
	// for the rest of the connection, which is the one outcome worth
	// preventing: the token is not a decoding failure, it is the server
	// saying nothing changed.
	if count == 0xffff {
		return nil, p, parseOK
	}

	cols = make([]column, 0, count)
	for range count {
		// UserType (4 bytes on TDS 7.2+) then Flags (2).
		if len(b)-p < 6 {
			return nil, 0, parseNeedMore
		}
		p += 6

		col, adv, cst := parseTypeInfo(b[p:])
		if cst != parseOK {
			return nil, 0, cst
		}
		p += adv

		// ColName: a B_VARCHAR, one byte of CHARACTER count.
		if len(b)-p < 1 {
			return nil, 0, parseNeedMore
		}
		nameLen := int(b[p]) * 2
		p++
		if len(b)-p < nameLen {
			return nil, 0, parseNeedMore
		}
		col.name = ucs2ToString(b[p : p+nameLen])
		p += nameLen

		cols = append(cols, col)
	}
	return cols, p, parseOK
}

// parseTypeInfo reads one column's TYPE_INFO and reports how a ROW will encode
// its value.
func parseTypeInfo(b []byte) (col column, n int, st parseStatus) {
	if len(b) < 1 {
		return column{}, 0, parseNeedMore
	}
	t := b[0]
	p := 1

	switch t {
	// ---- fixed width, no length prefix in the row ----
	case tNull:
		return column{kind: lenFixed, width: 0}, p, parseOK
	case tInt1, tBit:
		return column{kind: lenFixed, width: 1}, p, parseOK
	case tInt2:
		return column{kind: lenFixed, width: 2}, p, parseOK
	case tInt4, tFlt4, tDateTim4, tMoney4:
		return column{kind: lenFixed, width: 4}, p, parseOK
	case tInt8, tFlt8, tMoney, tDateTime:
		return column{kind: lenFixed, width: 8}, p, parseOK

	// ---- BYTE length in the row; TYPE_INFO carries a max size ----
	case tIntN, tBitN, tFltN, tMoneyN, tDateTimeN, tGuid, tBinary, tVarBinary:
		if len(b)-p < 1 {
			return column{}, 0, parseNeedMore
		}
		return column{kind: lenByte}, p + 1, parseOK

	case tDecimal, tNumeric, tDecimalN, tNumericN:
		// size, precision, scale
		if len(b)-p < 3 {
			return column{}, 0, parseNeedMore
		}
		return column{kind: lenByte}, p + 3, parseOK

	case tDateN:
		return column{kind: lenByte}, p, parseOK

	case tTimeN, tDateTime2N, tDTOffsetN:
		if len(b)-p < 1 {
			return column{}, 0, parseNeedMore
		}
		return column{kind: lenByte}, p + 1, parseOK

	// ---- legacy single-byte CHAR/VARCHAR: BYTE length, and maskable ----
	case tChar, tVarChar:
		if len(b)-p < 1 {
			return column{}, 0, parseNeedMore
		}
		return column{kind: lenByte, text: true}, p + 1, parseOK

	// ---- USHORT length, or PLP when the declared max is 0xFFFF ----
	case tBigVarChar, tBigChar, tNVarChar, tNChar:
		// max size (2) + collation (5)
		if len(b)-p < 7 {
			return column{}, 0, parseNeedMore
		}
		maxSize := binary.LittleEndian.Uint16(b[p : p+2])
		p += 7
		c := column{kind: lenUShort, text: true, ucs2: t == tNVarChar || t == tNChar}
		if maxSize == 0xffff {
			c.kind = lenPLP
		}
		return c, p, parseOK

	case tBigVarBin, tBigBinary:
		if len(b)-p < 2 {
			return column{}, 0, parseNeedMore
		}
		maxSize := binary.LittleEndian.Uint16(b[p : p+2])
		p += 2
		c := column{kind: lenUShort}
		if maxSize == 0xffff {
			c.kind = lenPLP
		}
		return c, p, parseOK

	// ---- TEXT/NTEXT/IMAGE: LONG length, preceded by a textptr in the row ----
	case tText, tNText:
		// max size (4) + collation (5) + table name (US_VARCHAR parts)
		if len(b)-p < 9 {
			return column{}, 0, parseNeedMore
		}
		p += 9
		adv, good := skipTableName(b[p:])
		if !good {
			return column{}, 0, parseNeedMore
		}
		return column{kind: lenLong, text: true, ucs2: t == tNText}, p + adv, parseOK

	case tImage:
		if len(b)-p < 4 {
			return column{}, 0, parseNeedMore
		}
		p += 4
		adv, good := skipTableName(b[p:])
		if !good {
			return column{}, 0, parseNeedMore
		}
		return column{kind: lenLong}, p + adv, parseOK

	default:
		// SQL_VARIANT, XML, UDT and anything newer. Their row encodings need
		// more than a length rule, so the rewriter steps aside for the whole
		// result set instead of desynchronizing on one column.
		return column{kind: lenUnknown}, p, parseCannot
	}
}

// skipTableName steps over the NumParts/US_VARCHAR table name that follows
// TEXT, NTEXT and IMAGE metadata.
func skipTableName(b []byte) (n int, ok bool) {
	if len(b) < 1 {
		return 0, false
	}
	parts := int(b[0])
	p := 1
	for range parts {
		if len(b)-p < 2 {
			return 0, false
		}
		chars := int(binary.LittleEndian.Uint16(b[p : p+2]))
		p += 2 + chars*2
		if len(b) < p {
			return 0, false
		}
	}
	return p, true
}
