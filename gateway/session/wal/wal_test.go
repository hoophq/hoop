package wal

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/session/eventlog"
	eventlogv0 "github.com/runopsio/hoop/gateway/session/eventlog/v0"
)

func newFakeHeader(orgID string) *Header {
	t := time.Now().UTC()
	return &Header{
		OrgID:          orgID,
		SessionID:      uuid.NewString(),
		ConnectionName: "fake-conn",
		ConnectionType: "command-line",
		StartDate:      &t,
	}
}

func date(hour, min int) time.Time {
	return time.Date(2023, time.August, 24, hour, min, 00, 00, time.UTC)
}

func TestReadWrite(t *testing.T) {
	expectedEventMap := map[time.Time]*eventlogv0.EventLog{
		date(10, 19): eventlogv0.New(date(10, 19), 'i', 0, []byte(`ls -l`)),
		date(10, 20): eventlogv0.New(date(10, 20), 'o', 2, []byte(`file01\nfile02\nfile02`)),
		date(10, 21): eventlogv0.New(date(10, 21), 'e', 1, []byte(`exit 1`)),
	}
	waldir := filepath.Join(os.TempDir(), "test-rw-%s.wal", uuid.NewString()[:8])
	walog, err := OpenWriteHeader(waldir, newFakeHeader("test-org"))
	if err != nil {
		t.Fatal(walog)
	}
	defer walog.Close()
	defer os.RemoveAll(waldir)

	for _, event := range expectedEventMap {
		err = walog.Write(event)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = walog.ReadAtMost(defaultMaxRead, func(eventLog []byte) error {
		got, err := eventlog.DecodeLatest(eventLog)
		if err != nil {
			t.Fatal(err)
		}
		want, ok := expectedEventMap[got.EventTime]
		if !ok {
			t.Errorf("expect to find the event time %v in the map", got.EventTime)
			return nil
		}

		if want.String() != got.String() {
			t.Errorf("event did not match, want=%v, got=%v", want.String(), got.String())
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
}

func TestWriteWithoutHavingHeader(t *testing.T) {
	waldir := filepath.Join(os.TempDir(), "test-rw-%s.wal", uuid.NewString()[:8])
	walog, err := Open(waldir)
	if err != nil {
		t.Fatal(walog)
	}
	defer walog.Close()
	defer os.RemoveAll(waldir)

	err = walog.Write(eventlogv0.New(date(10, 19), 'i', 0, []byte(`ls -l`)))
	if err != ErrExpectHeader {
		t.Errorf("expected error, want=%v, got=%v", ErrExpectHeader, err)
	}
}

func TestWriteHeaderMultipleTimes(t *testing.T) {
	waldir := filepath.Join(os.TempDir(), "test-rw-%s.wal", uuid.NewString()[:8])
	walog, err := OpenWriteHeader(waldir, newFakeHeader("test-org"))
	if err != nil {
		t.Fatal(walog)
	}
	defer walog.Close()
	defer os.RemoveAll(waldir)

	err = walog.WriteHeader(newFakeHeader("test-org"))
	if err != ErrHeaderAlreadyWritten {
		t.Errorf("expected error, want=%v, got=%v", ErrHeaderAlreadyWritten, err)
	}
	err = walog.Write(eventlogv0.New(date(10, 19), 'i', 0, []byte(`ls -l`)))
	if err != nil {
		t.Errorf("got error when writing event to wal, err=%v", err)
	}
}
