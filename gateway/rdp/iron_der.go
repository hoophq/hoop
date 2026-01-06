package rdp

import (
	"encoding/asn1"
	"errors"
)

// unmarshalContextExplicit fills 'out' (a pointer to RDCleanPathPdu) from a DER
// value that is a SEQUENCE whose elements are context-specific EXPLICIT tags.
func unmarshalContextExplicit(der []byte, out *RDCleanPathPdu) error {
	if out == nil {
		return errors.New("out must be a non-nil pointer")
	}

	// read the top-level SEQUENCE
	var root asn1.RawValue
	if _, err := asn1.Unmarshal(der, &root); err != nil {
		return err
	}
	if root.Class != 0 || root.Tag != 16 || !root.IsCompound {
		return errors.New("expected top-level SEQUENCE")
	}

	// extract children grouped by context-specific tag
	childrenByTag := map[int]asn1.RawValue{}
	inner := root.Bytes
	for len(inner) > 0 {
		var child asn1.RawValue
		rest, err := asn1.Unmarshal(inner, &child)
		if err != nil {
			return err
		}
		if child.Class == 2 { // context-specific
			childrenByTag[child.Tag] = child
		}
		inner = rest
	}

	// Unmarshal tag 0: Version (uint64)
	if child, ok := childrenByTag[0]; ok {
		var val int64
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		out.Version = uint64(val)
	}

	// Unmarshal tag 1: Error (*RDCleanPathError)
	if child, ok := childrenByTag[1]; ok {
		var err RDCleanPathError
		if e := unmarshalRDCleanPathError(child.Bytes, &err); e != nil {
			return e
		}
		out.Error = &err
	}

	// Unmarshal tag 2: Destination (*string)
	if child, ok := childrenByTag[2]; ok {
		var val string
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		out.Destination = &val
	}

	// Unmarshal tag 3: ProxyAuth (*string)
	if child, ok := childrenByTag[3]; ok {
		var val string
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		out.ProxyAuth = &val
	}

	// Unmarshal tag 4: ServerAuth (*string)
	if child, ok := childrenByTag[4]; ok {
		var val string
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		out.ServerAuth = &val
	}

	// Unmarshal tag 5: PreconnectionBlob (*string)
	if child, ok := childrenByTag[5]; ok {
		var val string
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		out.PreconnectionBlob = &val
	}

	// Unmarshal tag 6: X224ConnectionPDU ([]byte)
	if child, ok := childrenByTag[6]; ok {
		var val []byte
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		out.X224ConnectionPDU = val
	}

	// Unmarshal tag 7: ServerCertChain ([][]byte)
	if child, ok := childrenByTag[7]; ok {
		var val [][]byte
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		out.ServerCertChain = val
	}

	// Unmarshal tag 9: ServerAddr (*string)
	if child, ok := childrenByTag[9]; ok {
		var val string
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		out.ServerAddr = &val
	}

	return nil
}

// unmarshalRDCleanPathError decodes a nested RDCleanPathError struct from DER bytes
func unmarshalRDCleanPathError(der []byte, out *RDCleanPathError) error {
	// read the SEQUENCE
	var root asn1.RawValue
	if _, err := asn1.Unmarshal(der, &root); err != nil {
		return err
	}
	if root.Class != 0 || root.Tag != 16 || !root.IsCompound {
		return errors.New("expected SEQUENCE for RDCleanPathError")
	}

	// extract children by tag
	childrenByTag := map[int]asn1.RawValue{}
	inner := root.Bytes
	for len(inner) > 0 {
		var child asn1.RawValue
		rest, err := asn1.Unmarshal(inner, &child)
		if err != nil {
			return err
		}
		if child.Class == 2 { // context-specific
			childrenByTag[child.Tag] = child
		}
		inner = rest
	}

	// Unmarshal tag 0: ErrorCode (uint16)
	if child, ok := childrenByTag[0]; ok {
		var val int64
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		out.ErrorCode = uint16(val)
	}

	// Unmarshal tag 1: HttpStatusCode (*uint16)
	if child, ok := childrenByTag[1]; ok {
		var val int64
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		v := uint16(val)
		out.HttpStatusCode = &v
	}

	// Unmarshal tag 2: WSALastError (*uint16)
	if child, ok := childrenByTag[2]; ok {
		var val int64
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		v := uint16(val)
		out.WSALastError = &v
	}

	// Unmarshal tag 3: TLSAlertCode (*uint16)
	if child, ok := childrenByTag[3]; ok {
		var val int64
		if _, err := asn1.Unmarshal(child.Bytes, &val); err != nil {
			return err
		}
		v := uint16(val)
		out.TLSAlertCode = &v
	}

	return nil
}

// --- Marshal implementation -------------------------------------------------

// marshalContextExplicit encodes RDCleanPathPdu or RDCleanPathError into DER format
// where each field becomes a context-specific EXPLICIT element.
func marshalContextExplicit(v interface{}) ([]byte, error) {
	switch val := v.(type) {
	case *RDCleanPathPdu:
		if val == nil {
			return nil, errors.New("nil pointer provided")
		}
		return marshalRDCleanPathPdu(val)
	case RDCleanPathPdu:
		return marshalRDCleanPathPdu(&val)
	case *RDCleanPathError:
		if val == nil {
			return nil, errors.New("nil pointer provided")
		}
		return marshalRDCleanPathError(val)
	case RDCleanPathError:
		return marshalRDCleanPathError(&val)
	default:
		return nil, errors.New("unsupported type for marshalContextExplicit")
	}
}

func marshalRDCleanPathPdu(pdu *RDCleanPathPdu) ([]byte, error) {
	var children [][]byte

	// tag 0: Version (uint64)
	inner := encodeInteger(pdu.Version)
	children = append(children, encodeContextExplicit(0, inner))

	// tag 1: Error (*RDCleanPathError)
	if pdu.Error != nil {
		inner, err := marshalRDCleanPathError(pdu.Error)
		if err != nil {
			return nil, err
		}
		children = append(children, encodeContextExplicit(1, inner))
	}

	// tag 2: Destination (*string)
	if pdu.Destination != nil {
		inner := encodeUTF8String(*pdu.Destination)
		children = append(children, encodeContextExplicit(2, inner))
	}

	// tag 3: ProxyAuth (*string)
	if pdu.ProxyAuth != nil {
		inner := encodeUTF8String(*pdu.ProxyAuth)
		children = append(children, encodeContextExplicit(3, inner))
	}

	// tag 4: ServerAuth (*string)
	if pdu.ServerAuth != nil {
		inner := encodeUTF8String(*pdu.ServerAuth)
		children = append(children, encodeContextExplicit(4, inner))
	}

	// tag 5: PreconnectionBlob (*string)
	if pdu.PreconnectionBlob != nil {
		inner := encodeUTF8String(*pdu.PreconnectionBlob)
		children = append(children, encodeContextExplicit(5, inner))
	}

	// tag 6: X224ConnectionPDU ([]byte)
	if pdu.X224ConnectionPDU != nil {
		inner, err := asn1.Marshal(pdu.X224ConnectionPDU)
		if err != nil {
			return nil, err
		}
		children = append(children, encodeContextExplicit(6, inner))
	}

	// tag 7: ServerCertChain ([][]byte)
	if pdu.ServerCertChain != nil {
		inner, err := asn1.Marshal(pdu.ServerCertChain)
		if err != nil {
			return nil, err
		}
		children = append(children, encodeContextExplicit(7, inner))
	}

	// tag 9: ServerAddr (*string)
	if pdu.ServerAddr != nil {
		inner := encodeUTF8String(*pdu.ServerAddr)
		children = append(children, encodeContextExplicit(9, inner))
	}

	// concatenate children and wrap in SEQUENCE
	payload := concat(children)
	seq := encodeSequence(payload)
	return seq, nil
}

func marshalRDCleanPathError(err *RDCleanPathError) ([]byte, error) {
	var children [][]byte

	// tag 0: ErrorCode (uint16)
	inner := encodeInteger(uint64(err.ErrorCode))
	children = append(children, encodeContextExplicit(0, inner))

	// tag 1: HttpStatusCode (*uint16)
	if err.HttpStatusCode != nil {
		inner := encodeInteger(uint64(*err.HttpStatusCode))
		children = append(children, encodeContextExplicit(1, inner))
	}

	// tag 2: WSALastError (*uint16)
	if err.WSALastError != nil {
		inner := encodeInteger(uint64(*err.WSALastError))
		children = append(children, encodeContextExplicit(2, inner))
	}

	// tag 3: TLSAlertCode (*uint16)
	if err.TLSAlertCode != nil {
		inner := encodeInteger(uint64(*err.TLSAlertCode))
		children = append(children, encodeContextExplicit(3, inner))
	}

	// concatenate children and wrap in SEQUENCE
	payload := concat(children)
	seq := encodeSequence(payload)
	return seq, nil
}

func encodeUTF8String(s string) []byte {
	content := []byte(s)
	lenEnc := encodeLength(len(content))
	out := make([]byte, 1+len(lenEnc)+len(content))
	out[0] = 0x0C // UTF8String tag (universal, primitive, tag 12)
	copy(out[1:], lenEnc)
	copy(out[1+len(lenEnc):], content)
	return out
}

func encodeContextExplicit(tag int, inner []byte) []byte {
	// tag class: context-specific (2), constructed bit set -> 0xA0 base
	if tag < 31 {
		header := byte(0xA0 | byte(tag))
		lenEnc := encodeLength(len(inner))
		out := make([]byte, 1+len(lenEnc)+len(inner))
		out[0] = header
		copy(out[1:], lenEnc)
		copy(out[1+len(lenEnc):], inner)
		return out
	}
	// fallback: not supporting high-tag-number encoding here
	return nil
}

func encodeSequence(payload []byte) []byte {
	header := byte(0x30) // universal constructed SEQUENCE
	lenEnc := encodeLength(len(payload))
	out := make([]byte, 1+len(lenEnc)+len(payload))
	out[0] = header
	copy(out[1:], lenEnc)
	copy(out[1+len(lenEnc):], payload)
	return out
}

func encodeLength(n int) []byte {
	if n < 128 {
		return []byte{byte(n)}
	}
	// long-form
	var tmp []byte
	for n > 0 {
		tmp = append([]byte{byte(n & 0xff)}, tmp...)
		n >>= 8
	}
	lenLen := byte(0x80 | byte(len(tmp)))
	return append([]byte{lenLen}, tmp...)
}

func concat(parts [][]byte) []byte {
	tot := 0
	for _, p := range parts {
		tot += len(p)
	}
	out := make([]byte, 0, tot)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// encodeInteger encodes a non-negative integer as a DER INTEGER (full TLV).
func encodeInteger(u uint64) []byte {
	// convert to big-endian bytes
	if u == 0 {
		return []byte{0x02, 0x01, 0x00}
	}
	var buf [8]byte
	n := 0
	for v := u; v > 0; v >>= 8 {
		buf[len(buf)-1-n] = byte(v & 0xff)
		n++
	}
	content := make([]byte, n)
	copy(content, buf[len(buf)-n:])
	// if high bit set, prepend a zero to indicate a positive integer
	if content[0]&0x80 != 0 {
		content = append([]byte{0x00}, content...)
	}
	// build TLV
	lenEnc := encodeLength(len(content))
	out := make([]byte, 1+len(lenEnc)+len(content))
	out[0] = 0x02
	copy(out[1:], lenEnc)
	copy(out[1+len(lenEnc):], content)
	return out
}
