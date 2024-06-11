package eventlogv1

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

var (
	ErrUnknownEventType            = errors.New("unknown event type")
	ErrDecodeMinimumLength         = errors.New("log does not have minium length")
	ErrPayloadLargerThanLog        = errors.New("the payload length is larger than the rest of log")
	ErrMissingMetadataLengthHeader = errors.New("missing metadata length header")
	ErrMetadataLargerThanLog       = errors.New("the metadata header length is larger than the rest of log")
	ErrHeaderLengthLargerThanLog   = errors.New("the header length is larger than the log object")
)

type EventType byte

const (
	InputType  EventType = 'i'
	OutputType EventType = 'o'
	ErrorType  EventType = 'e'

	commitErrKeyName string = "__commit_error"

	Version = "v1"
)

type EventLog struct {
	EventTime time.Time
	EventType EventType
	Payload   []byte
	metadata  map[string][]byte
}

func New(eventTime time.Time, eventType EventType, payload []byte, metadata map[string][]byte) *EventLog {
	if metadata == nil {
		metadata = map[string][]byte{}
	}
	// coerce nil to empty string
	if payload == nil {
		payload = []byte(``)
	}
	for key, val := range metadata {
		if val == nil {
			metadata[key] = []byte(``)
		}
	}
	return &EventLog{
		EventTime: eventTime,
		EventType: eventType,
		Payload:   payload,
		metadata:  metadata,
	}
}

// NewCommitError event is used to be handled differently when reading it
// in a sequence of logs.
func NewCommitError(eventTime time.Time, errPayload string) *EventLog {
	return &EventLog{
		EventTime: eventTime,
		EventType: ErrorType,
		Payload:   []byte(errPayload),
		metadata:  map[string][]byte{commitErrKeyName: []byte("1")},
	}
}

func (e *EventLog) WithMetadata(key string, val []byte) *EventLog {
	e.metadata[key] = val
	return e
}

func (e *EventLog) GetMetadata(key string) []byte {
	if e.metadata == nil {
		return nil
	}
	return e.metadata[key]
}

func (e *EventLog) IsCommitErr() bool { return len(e.GetMetadata(commitErrKeyName)) > 0 }
func (e *EventLog) String() string {
	return fmt.Sprintf("time=%v,type=%v,commit-err=%v,metadata=%v,payload=%v",
		e.EventTime, string(e.EventType), e.IsCommitErr(), len(e.metadata), len(e.Payload))
}

func (e *EventLog) logSize() int {
	headerSize := 4 + 4              // full payload size header + payload size header
	headerSize += 1 + 8              // event type + event time
	headerSize += len(e.Payload) + 1 // payload content + 0x00 delimiter
	for key, val := range e.metadata {
		headerSize += 4            // metadata header size
		headerSize += len(key) + 1 // key content + 0x00 delimiter
		headerSize += len(val) + 1 // val content + 0x00 delimiter
	}
	return headerSize
}

func (e *EventLog) Encode() ([]byte, error) {
	if e.EventType != InputType && e.EventType != OutputType && e.EventType != ErrorType {
		return nil, ErrUnknownEventType
	}
	fullLogSize := e.logSize()

	// full payload header
	data := make([]byte, fullLogSize)
	binary.BigEndian.PutUint32(data[0:4], uint32(fullLogSize))

	// payload header and event type
	binary.BigEndian.PutUint32(data[4:8], uint32(len(e.Payload)))
	data[8] = byte(e.EventType)

	// event time
	binary.BigEndian.PutUint64(data[9:17], uint64(e.EventTime.UnixNano()))
	if copied := copy(data[17:], e.Payload); copied != len(e.Payload) {
		return nil, fmt.Errorf("failed copying payload, copied=%v, length=%v", copied, len(e.Payload))
	}

	pos := 17 + len(e.Payload) + 1 // 0x00 delimiter
	for key, val := range e.metadata {
		metadataSize := uint32(len(key) + 1 + len(val) + 1)
		binary.BigEndian.PutUint32(data[pos:pos+4], metadataSize)
		pos += 4
		_ = copy(data[pos:], []byte(key))
		pos += len(key) + 1
		_ = copy(data[pos:], []byte(val))
		pos += len(val) + 1
	}
	return data, nil
}

func Decode(data []byte) (*EventLog, error) {
	if len(data) < 18 {
		return nil, ErrDecodeMinimumLength
	}
	fullLogSize := binary.BigEndian.Uint32(data[0:4])
	payloadSize := binary.BigEndian.Uint32(data[4:8])
	if int(fullLogSize) > len(data) {
		return nil, ErrHeaderLengthLargerThanLog
	}

	event := EventLog{metadata: map[string][]byte{}}
	event.EventType = EventType(data[8])
	event.EventTime = time.Unix(0, int64(binary.BigEndian.Uint64(data[9:17]))).In(time.UTC)

	if int(payloadSize) > len(data[17:]) {
		return nil, ErrPayloadLargerThanLog
	}
	event.Payload = data[17 : 17+payloadSize]

	pos := payloadSize + 17 + 1
	if pos == fullLogSize {
		return &event, nil
	}
	for {
		if pos == fullLogSize {
			return &event, nil
		}
		if pos > fullLogSize {
			return nil, fmt.Errorf("unable to decode header, reached max position=%v, full-log-size=%v", pos, fullLogSize)
		}
		if len(data[pos:]) < 4 {
			return nil, ErrMissingMetadataLengthHeader
		}
		metadataLength := binary.BigEndian.Uint32(data[pos : pos+4])
		pos += 4
		if int(metadataLength) > len(data[pos:]) {
			return nil, ErrMetadataLargerThanLog
		}

		metadataPayload := data[pos : pos+metadataLength]
		// We only expect keys to be utf-8 strings, so it should
		// be safe to rely in the 0x00 delimiter. However for value
		// it's not safe because it could contain any data.
		keyIdx := bytes.IndexByte(metadataPayload, 0x00)
		key := string(metadataPayload[:keyIdx])
		// remove 0x00 delimiter
		val := metadataPayload[keyIdx+1:]
		event.metadata[key] = val[:len(val)-1]
		pos += metadataLength
	}
}
