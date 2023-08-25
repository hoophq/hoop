package eventlogv0

import (
	"testing"
	"time"
)

func date(hour, min int) time.Time {
	return time.Date(2023, time.August, 24, hour, min, 00, 00, time.UTC)
}

func TestEncodeDecode(t *testing.T) {
	expectedEventList := []*EventLog{
		New(date(10, 19), InputType, 0, []byte(`ls -l`)),
		New(date(10, 20), OutputType, 2, []byte(`file01\nfile02\nfile02`)),
		New(date(10, 21), ErrorType, 1, []byte(`exit 1`)),
	}
	for _, want := range expectedEventList {
		encEvent, err := want.Encode()
		if err != nil {
			t.Errorf("encoding event error=%v", err)
			continue
		}
		decEvent, err := Decode(encEvent)
		if err != nil {
			t.Errorf("decoding event error=%v", err)
			continue
		}
		if want.String() != decEvent.String() {
			t.Errorf("decoded event does not match, want=%v, got=%v",
				want.String(), decEvent.String())
		}
	}
}

func TestCommitErrorEncodeDecode(t *testing.T) {
	expectedEventList := []*EventLog{
		New(date(10, 19), InputType, 0, []byte(`sleep 60`)),
		New(date(10, 19), InputType, 0, []byte(`echo OK`)),
		New(date(10, 20), OutputType, 2, []byte(`OK\n`)),
		New(date(10, 20), OutputType, 2, []byte(`OK\n`)),
		{
			CommitError:   "failed persisting data",
			CommitEndDate: func() *time.Time { t := date(10, 20); return &t }(),
		},
	}
	for _, want := range expectedEventList {
		encEvent, err := want.Encode()
		if err != nil {
			t.Errorf("encoding event error=%v", err)
			continue
		}
		decEvent, err := Decode(encEvent)
		if err != nil {
			t.Errorf("decoding event error=%v", err)
			continue
		}
		if want.String() != decEvent.String() {
			t.Errorf("decoded event does not match, want=%v, got=%v",
				want.String(), decEvent.String())
		}
	}
}
