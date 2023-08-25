package eventlogv0

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"
)

var ErrUnknownEventType = fmt.Errorf("unknown event type")

type EventType byte

const (
	InputType  EventType = 'i'
	OutputType EventType = 'o'
	ErrorType  EventType = 'e'
)

type EventLog struct {
	EventTime   time.Time `json:"-"`
	EventType   EventType `json:"-"`
	RedactCount uint64    `json:"-"`
	Data        []byte    `json:"-"`

	// commit errors are used to indicate
	// the event log was not commited in the storage
	CommitError   string     `json:"commit_error"`
	CommitEndDate *time.Time `json:"end_date"`
}

func New(eventTime time.Time, eventType EventType, redactCount uint64, data []byte) *EventLog {
	return &EventLog{eventTime, eventType, redactCount, data, "", nil}
}

func (e *EventLog) String() string {
	var endDate string
	if e.CommitEndDate != nil {
		endDate = e.CommitEndDate.Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("time=%v,type=%v,redact=%v,data=%X,error=%v,end_date=%v",
		e.EventTime, string(e.EventType), e.RedactCount, e.Data, e.CommitError, endDate)
}

func (e *EventLog) Encode() ([]byte, error) {
	if e.CommitEndDate != nil || e.CommitError != "" {
		return json.Marshal(e)
	}
	if e.EventType != InputType && e.EventType != OutputType && e.EventType != ErrorType {
		return nil, ErrUnknownEventType
	}
	var encodedRedactCount [8]byte
	binary.BigEndian.PutUint64(encodedRedactCount[:], uint64(e.RedactCount))
	eventHeader := append(
		[]byte(e.EventTime.Format(time.RFC3339Nano)),
		'\000',
		byte(e.EventType),
		'\000',
	)
	eventHeader = append(eventHeader, encodedRedactCount[:]...)
	eventHeader = append(eventHeader, '\000')
	return append(eventHeader, e.Data...), nil
}

func Decode(data []byte) (*EventLog, error) {
	if bytes.HasPrefix(data, []byte(`{"commit_error":`)) {
		var ev EventLog
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil, fmt.Errorf("failed decoding commit info: %v", err)
		}
		return &ev, nil
	}

	position := bytes.IndexByte(data, '\000')
	if position == -1 {
		return nil, fmt.Errorf("event stream in wrong format [event-time]")
	}
	eventTimeBytes := data[:position]
	eventTime, err := time.Parse(time.RFC3339Nano, string(eventTimeBytes))
	if err != nil {
		return nil, fmt.Errorf("failed parsing event time, err=%v", err)
	}

	position += 2
	if len(data) <= position {
		return nil, fmt.Errorf("event stream in wrong format [event-type]")
	}
	eventType := data[position-1]

	// dlp counter uses 8-byte (int64)
	position += 9
	if len(data) <= position {
		return nil, fmt.Errorf("event stream in wrong format [event-type]")
	}
	redactCount := binary.BigEndian.Uint64(data[position-8 : position])
	return &EventLog{
		EventTime:   eventTime,
		EventType:   EventType(eventType),
		RedactCount: redactCount,
		Data:        data[position+1:],
	}, nil
}
