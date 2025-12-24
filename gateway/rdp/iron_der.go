package rdp

import (
	"encoding/asn1"
	"errors"
	"reflect"
	"strconv"
	"strings"
)

// UnmarshalContextExplicit fills 'out' (a pointer to struct) from a DER
// value that is a SEQUENCE whose elements are context-specific EXPLICIT tags.
// Struct fields must use tags like `asn1:"tag:0"` to map context tags to fields.
func UnmarshalContextExplicit(der []byte, out interface{}) error {
	// out must be a non-nil pointer to a struct
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return errors.New("out must be a non-nil pointer to a struct")
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return errors.New("out must point to a struct")
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
	childrenByTag := map[int][]asn1.RawValue{}
	inner := root.Bytes
	for len(inner) > 0 {
		var child asn1.RawValue
		rest, err := asn1.Unmarshal(inner, &child)
		if err != nil {
			return err
		}
		if child.Class == 2 || child.Class == 0 { // context-specific
			childrenByTag[child.Tag] = append(childrenByTag[child.Tag], child)
		}
		// advance to rest returned by asn1.Unmarshal
		inner = rest
	}

	// map struct fields by tag and unmarshal
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tagStr := sf.Tag.Get("asn1")
		if tagStr == "" {
			continue
		}
		// find "tag:N" part
		var tagNum int = -1
		parts := strings.Split(tagStr, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "tag:") {
				n, err := strconv.Atoi(strings.TrimPrefix(p, "tag:"))
				if err == nil {
					tagNum = n
				}
			}
		}
		if tagNum < 0 {
			continue
		}

		fieldVal := v.Field(i)
		if !fieldVal.CanSet() {
			continue
		}

		children, ok := childrenByTag[tagNum]
		if !ok || len(children) == 0 {
			// no value present -> skip (optional field)
			continue
		}

		// helper to unmarshal the explicit inner bytes of a child RawValue
		unmarshalInner := func(rv asn1.RawValue, target interface{}) error {
			_, err := asn1.Unmarshal(rv.Bytes, target)
			return err
		}

		ft := sf.Type
		// special-case for asn1.RawValue and *asn1.RawValue
		if ft == reflect.TypeOf(asn1.RawValue{}) {
			var rr asn1.RawValue
			if err := unmarshalInner(children[0], &rr); err != nil {
				return err
			}
			fieldVal.Set(reflect.ValueOf(rr))
			continue
		}
		if ft.Kind() == reflect.Ptr && ft.Elem() == reflect.TypeOf(asn1.RawValue{}) {
			var rr asn1.RawValue
			if err := unmarshalInner(children[0], &rr); err != nil {
				return err
			}
			ptr := reflect.New(reflect.TypeOf(asn1.RawValue{}))
			ptr.Elem().Set(reflect.ValueOf(rr))
			fieldVal.Set(ptr)
			continue
		}

		// special-case: struct types -> use recursive UnmarshalContextExplicit
		if ft.Kind() == reflect.Struct {
			// decode into the struct value (addressable)
			if err := UnmarshalContextExplicit(children[0].Bytes, fieldVal.Addr().Interface()); err != nil {
				return err
			}
			continue
		}
		if ft.Kind() == reflect.Ptr && ft.Elem().Kind() == reflect.Struct {
			// create a pointer to an element and unmarshal into it
			ptr := reflect.New(ft.Elem())
			if err := UnmarshalContextExplicit(children[0].Bytes, ptr.Interface()); err != nil {
				return err
			}
			fieldVal.Set(ptr)
			continue
		}

		switch ft.Kind() {
		case reflect.Ptr:
			// create a new element and unmarshal into it
			elemType := ft.Elem()
			// special-case unsigned integer pointer types: decode into int64 then convert
			if elemType.Kind() >= reflect.Uint && elemType.Kind() <= reflect.Uint64 {
				var vi int64
				if _, err := asn1.Unmarshal(children[0].Bytes, &vi); err != nil {
					return err
				}
				ptr := reflect.New(elemType)
				ptr.Elem().SetUint(uint64(vi))
				fieldVal.Set(ptr)
				continue
			}
			ptr := reflect.New(elemType)
			if _, err := asn1.Unmarshal(children[0].Bytes, ptr.Interface()); err != nil {
				return err
			}
			fieldVal.Set(ptr)
		case reflect.Slice:
			// if there's a single context child, try to unmarshal it directly as a SEQUENCE OF
			if len(children) == 1 {
				slicePtr := reflect.New(ft)
				if _, err := asn1.Unmarshal(children[0].Bytes, slicePtr.Interface()); err == nil {
					fieldVal.Set(slicePtr.Elem())
					continue
				}
			}
			// multiple context-specific occurrences: build slice from each child
			elemType := ft.Elem()
			sliceVal := reflect.MakeSlice(ft, 0, len(children))
			// special case: target is []byte
			if elemType.Kind() == reflect.Uint8 {
				// field is []byte; expect a single child representing an OCTET STRING.
				if len(children) != 1 {
					return errors.New("unexpected multiple elements for []byte field")
				}
				var b []byte
				if _, err := asn1.Unmarshal(children[0].Bytes, &b); err != nil {
					fieldVal.SetBytes(children[0].Bytes)
					continue
				}
				// set bytes directly
				fieldVal.SetBytes(b)
				continue
			}

			// handle unsigned integer element types specially by decoding as int64 then converting
			if elemType.Kind() >= reflect.Uint && elemType.Kind() <= reflect.Uint64 {
				for _, ch := range children {
					var vi int64
					if _, err := asn1.Unmarshal(ch.Bytes, &vi); err != nil {
						return err
					}
					elemPtr := reflect.New(elemType)
					elemPtr.Elem().SetUint(uint64(vi))
					sliceVal = reflect.Append(sliceVal, elemPtr.Elem())
				}
				fieldVal.Set(sliceVal)
				continue
			}

			// handle slice elements that are structs by recursive unmarshal
			if elemType.Kind() == reflect.Struct {
				for _, ch := range children {
					elemPtr := reflect.New(elemType)
					if err := UnmarshalContextExplicit(ch.Bytes, elemPtr.Interface()); err != nil {
						return err
					}
					sliceVal = reflect.Append(sliceVal, elemPtr.Elem())
				}
				fieldVal.Set(sliceVal)
				continue
			}

			for _, ch := range children {
				elemPtr := reflect.New(elemType)
				if _, err := asn1.Unmarshal(ch.Bytes, elemPtr.Interface()); err != nil {
					return err
				}
				sliceVal = reflect.Append(sliceVal, elemPtr.Elem())
			}
			fieldVal.Set(sliceVal)
		default:
			// direct type (int, uint64, string, []byte already handled above etc.)
			// special-case unsigned integers: decode into int64 then convert
			if ft.Kind() >= reflect.Uint && ft.Kind() <= reflect.Uint64 {
				var vi int64
				if _, err := asn1.Unmarshal(children[0].Bytes, &vi); err != nil {
					return err
				}
				fieldVal.SetUint(uint64(vi))
				continue
			}
			ptr := reflect.New(ft)
			if _, err := asn1.Unmarshal(children[0].Bytes, ptr.Interface()); err != nil {
				return err
			}
			fieldVal.Set(ptr.Elem())
		}
	}

	return nil
}

// --- Marshal implementation -------------------------------------------------

// MarshalContextExplicit encodes a struct (pointer or value) into DER where each
// struct field with tag `asn1:"tag:N"` becomes a context-specific EXPLICIT
// element [N] containing the normal DER encoding of the field value. The fields
// are emitted in struct field order. Slices are encoded as a single SEQUENCE OF
// unless the element is []byte (OCTET STRING), which is encoded as a single
// OCTET STRING wrapper.
func MarshalContextExplicit(v interface{}) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, errors.New("nil pointer provided")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, errors.New("value must be a struct or pointer to struct")
	}

	var children [][]byte
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tagStr := sf.Tag.Get("asn1")
		if tagStr == "" {
			continue
		}
		// extract tag:N
		var tagNum int = -1
		parts := strings.Split(tagStr, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "tag:") {
				n, err := strconv.Atoi(strings.TrimPrefix(p, "tag:"))
				if err == nil {
					tagNum = n
				}
			}
		}
		if tagNum < 0 {
			continue
		}

		fv := rv.Field(i)
		if !fv.IsValid() || (fv.Kind() == reflect.Ptr && fv.IsNil()) {
			// skip nil/invalid
			continue
		}

		// obtain inner encoding
		inner, err := marshalInnerValue(fv)
		if err != nil {
			return nil, err
		}
		// wrap as context-specific EXPLICIT
		child := encodeContextExplicit(tagNum, inner)
		children = append(children, child)
	}

	// concatenate children and wrap in SEQUENCE
	payload := concat(children)
	seq := encodeSequence(payload)
	return seq, nil
}

func marshalInnerValue(fv reflect.Value) ([]byte, error) {
	ft := fv.Type()
	// deref pointer
	if ft.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return nil, nil
		}
		fv = fv.Elem()
		ft = fv.Type()
	}

	// special-case for asn1.RawValue
	if ft == reflect.TypeOf(asn1.RawValue{}) {
		rv := fv.Interface().(asn1.RawValue)
		if len(rv.FullBytes) > 0 {
			return rv.FullBytes, nil
		}
		// construct raw encoding from fields
		return encodeRawValue(rv), nil
	}

	// if it's a struct, encode using MarshalContextExplicit (recursively handle tags)
	if ft.Kind() == reflect.Struct {
		// Use recursive context-explicit marshal for nested structs so unsigned integer fields are encoded correctly
		return MarshalContextExplicit(fv.Interface())
	}

	// strings should be encoded as UTF8String, not PrintableString
	if ft.Kind() == reflect.String {
		return encodeUTF8String(fv.String()), nil
	}

	if ft.Kind() >= reflect.Uint && ft.Kind() <= reflect.Uint64 {
		u := fv.Uint()
		return encodeInteger(u), nil
	}

	// slices: let asn1.Marshal handle SEQUENCE OF unless it's []byte or []string
	if ft.Kind() == reflect.Slice {
		if ft.Elem().Kind() == reflect.Uint8 {
			// []byte -> OCTET STRING
			b := fv.Bytes()
			return asn1.Marshal(b)
		}
		if ft.Elem().Kind() == reflect.String {
			// build SEQUENCE OF UTF8String
			var parts [][]byte
			for i := 0; i < fv.Len(); i++ {
				parts = append(parts, encodeUTF8String(fv.Index(i).String()))
			}
			payload := concat(parts)
			return encodeSequence(payload), nil
		}
		return asn1.Marshal(fv.Interface())
	}

	// fallback: rely on asn1.Marshal for ints, strings handled above, etc.
	return asn1.Marshal(fv.Interface())
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

func encodeRawValue(rv asn1.RawValue) []byte {
	// construct header for rv.Class, rv.IsCompound, rv.Tag (assumes tag < 31)
	classBits := byte(rv.Class) << 6
	constructed := byte(0)
	if rv.IsCompound {
		constructed = 0x20
	}
	if rv.Tag < 31 {
		header := classBits | constructed | byte(rv.Tag)
		lenEnc := encodeLength(len(rv.Bytes))
		out := make([]byte, 1+len(lenEnc)+len(rv.Bytes))
		out[0] = header
		copy(out[1:], lenEnc)
		copy(out[1+len(lenEnc):], rv.Bytes)
		return out
	}
	// not handling a high-tag-number form for raw value
	return nil
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
