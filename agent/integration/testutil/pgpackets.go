//go:build integration

package testutil

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	pgProtocolVersion3 = 196608
	pgProtocolSSL      = 80877103
)

func PGSSLRequest() []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], pgProtocolSSL)
	binary.BigEndian.PutUint32(buf[4:8], 4)
	return buf
}

func PGStartupMessage(user, database string) []byte {
	params := fmt.Sprintf("user\000%s\000database\000%s\000\000", user, database)
	bodyLen := 4 + len(params)
	buf := make([]byte, 4+bodyLen)
	binary.BigEndian.PutUint32(buf[0:4], uint32(bodyLen))
	copy(buf[4:], params)
	return buf
}

func PGSimpleQuery(sql string) []byte {
	queryBytes := []byte(sql + "\000")
	totalLen := 4 + len(queryBytes)
	buf := make([]byte, 1+totalLen)
	buf[0] = 'Q'
	binary.BigEndian.PutUint32(buf[1:5], uint32(totalLen))
	copy(buf[5:], queryBytes)
	return buf
}

func PGTerminate() []byte {
	buf := make([]byte, 5)
	buf[0] = 'X'
	binary.BigEndian.PutUint32(buf[1:5], 4)
	return buf
}

type PGMessage struct {
	Type    byte
	Payload []byte
}

func ParsePGMessages(data []byte) []PGMessage {
	var msgs []PGMessage
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		typ, err := r.ReadByte()
		if err != nil {
			break
		}
		if r.Len() < 4 {
			break
		}
		var lenBuf [4]byte
		if _, err := r.Read(lenBuf[:]); err != nil {
			break
		}
		frameLen := int(binary.BigEndian.Uint32(lenBuf[:])) - 4
		if frameLen < 0 || r.Len() < frameLen {
			break
		}
		frame := make([]byte, frameLen)
		if _, err := r.Read(frame); err != nil {
			break
		}
		msgs = append(msgs, PGMessage{Type: typ, Payload: frame})
	}
	return msgs
}

func (m PGMessage) IsAuthenticationOK() bool {
	if m.Type != 'R' {
		return false
	}
	if len(m.Payload) < 4 {
		return false
	}
	status := binary.BigEndian.Uint32(m.Payload[0:4])
	return status == 0
}

func (m PGMessage) IsReadyForQuery() bool {
	return m.Type == 'Z'
}

func (m PGMessage) ReadyStatus() byte {
	if !m.IsReadyForQuery() || len(m.Payload) < 1 {
		return 0
	}
	return m.Payload[0]
}

func (m PGMessage) AsRowDescription() []string {
	if m.Type != 'T' {
		return nil
	}
	r := bytes.NewReader(m.Payload)
	var numFields int16
	if err := binary.Read(r, binary.BigEndian, &numFields); err != nil {
		return nil
	}
	var names []string
	for i := 0; i < int(numFields); i++ {
		if name, err := readCString(r); err == nil {
			names = append(names, name)
		} else {
			break
		}
		if _, err := r.Seek(int64(18), 1); err != nil {
			break
		}
	}
	return names
}

func (m PGMessage) AsDataRow() [][]byte {
	if m.Type != 'D' {
		return nil
	}
	r := bytes.NewReader(m.Payload)
	var numCols int16
	if err := binary.Read(r, binary.BigEndian, &numCols); err != nil {
		return nil
	}
	var vals [][]byte
	for i := 0; i < int(numCols); i++ {
		var colLen int32
		if err := binary.Read(r, binary.BigEndian, &colLen); err != nil {
			break
		}
		if colLen == -1 {
			vals = append(vals, nil)
			continue
		}
		val := make([]byte, colLen)
		if _, err := r.Read(val); err != nil {
			break
		}
		vals = append(vals, val)
	}
	return vals
}

func (m PGMessage) AsCommandComplete() string {
	if m.Type != 'C' {
		return ""
	}
	return readCStringFromBytes(m.Payload)
}

func (m PGMessage) AsErrorResponse() map[byte]string {
	if m.Type != 'E' {
		return nil
	}
	r := bytes.NewReader(m.Payload)
	fields := make(map[byte]string)
	for r.Len() > 0 {
		ftype, err := r.ReadByte()
		if err != nil {
			break
		}
		if ftype == 0 {
			break
		}
		val, err := readCString(r)
		if err != nil {
			break
		}
		fields[ftype] = val
	}
	return fields
}

func readCString(r *bytes.Reader) (string, error) {
	var buf bytes.Buffer
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == 0 {
			return buf.String(), nil
		}
		buf.WriteByte(b)
	}
}

func readCStringFromBytes(data []byte) string {
	idx := bytes.IndexByte(data, 0)
	if idx == -1 {
		return string(data)
	}
	return string(data[:idx])
}

type ColumnDesc struct {
	Name     string
	TableOID uint32
	AttrNum  uint16
	TypeOID  uint32
	TypeSize int16
	TypeMod  int32
}
