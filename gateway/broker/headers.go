package broker

import (
	"encoding/binary"
	"fmt"

	"github.com/google/uuid"
)

// Header represents the session header for framing
type Header struct {
	SID uuid.UUID
	Len uint32
}

func (h *Header) Encode() []byte {
	buf := make([]byte, 20) // 16 bytes for UUID + 4 bytes for length
	copy(buf[:16], h.SID[:])
	binary.BigEndian.PutUint32(buf[16:20], h.Len)
	return buf
}

// Decode decodes a header from bytes
func DecodeHeader(data []byte) (*Header, int, error) {
	if len(data) < 20 {
		return nil, 0, fmt.Errorf("insufficient data for header")
	}

	var sid uuid.UUID
	copy(sid[:], data[:16])
	len := binary.BigEndian.Uint32(data[16:20])

	return &Header{
		SID: sid,
		Len: len,
	}, 20, nil
}
