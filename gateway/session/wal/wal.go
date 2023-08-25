package wal

import (
	"encoding/json"
	"fmt"

	"github.com/runopsio/hoop/gateway/session/eventlog"
	"github.com/tidwall/wal"
)

const (
	defaultHeaderIndex = 1 << iota
	defaultDataIndex

	defaultMaxRead = 110 * 1024 * 1024 // 110MB
)

var (
	ErrExpectHeader         = fmt.Errorf("expect having header before writing data")
	ErrHeaderAlreadyWritten = fmt.Errorf("header is already written")
)

type ReaderFunc func(eventLog []byte) error

type WalLog struct {
	filePath   string
	wlog       *wal.Log
	writeIndex uint64
}

// Open opens a wal file with default options, it's up to the caller to close the wal file
func Open(filePath string) (*WalLog, error) {
	wlog, err := wal.Open(filePath, wal.DefaultOptions)
	if err != nil {
		return nil, err
	}
	return &WalLog{filePath: filePath, writeIndex: defaultHeaderIndex, wlog: wlog}, nil
}

// OpenWithHeader opens the wallog and write the header, it's up to the caller to close the wal file
func OpenWriteHeader(filepPath string, h *Header) (*WalLog, error) {
	wlog, err := Open(filepPath)
	if err != nil {
		return nil, err
	}
	return wlog, wlog.WriteHeader(h)
}

// OpenWithHeader opens the wallog and returns the header of the log, it's up to the caller to close the wal file
func OpenWithHeader(filepPath string) (wlog *WalLog, h *Header, err error) {
	wlog, err = Open(filepPath)
	if err != nil {
		return
	}
	h, err = wlog.Header()
	return
}

// Header retrieves the header from the wal file
func (w *WalLog) Header() (*Header, error) {
	data, err := w.wlog.Read(defaultHeaderIndex)
	if err != nil {
		return nil, err
	}
	var h Header
	return &h, json.Unmarshal(data, &h)
}

// WriteHeader writes h in the first position of the log
func (w *WalLog) WriteHeader(h *Header) error {
	if w.writeIndex > defaultHeaderIndex {
		return ErrHeaderAlreadyWritten
	}
	if err := h.Validate(); err != nil {
		return err
	}
	encHeader, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if err := w.wlog.Write(w.writeIndex, encHeader); err != nil {
		return err
	}
	w.writeIndex++
	return nil
}

// Write add the event to the write ahead log file. It supports writing
// any object that implements the eventlog.Encoder interface
func (w *WalLog) Write(event eventlog.Encoder) error {
	if w.writeIndex == defaultHeaderIndex {
		return ErrExpectHeader
	}
	data, err := event.Encode()
	if err != nil {
		return err
	}
	if err := w.wlog.Write(w.writeIndex, data); err != nil {
		return err
	}
	w.writeIndex++
	return nil
}

// ReadAtMost reads event logs from a write ahead log up to max bytes specified.
// It returns a boolean indicading if it's truncated. It's up to the caller to
// decode the event log properly using a decoder.
func (w *WalLog) ReadAtMost(max uint32, readerFn ReaderFunc) (bool, error) {
	readBytes := uint32(0)
	truncated := false
	for i := defaultDataIndex; ; i++ {
		if readBytes > max {
			truncated = true
			break
		}
		eventStreamBytes, err := w.wlog.Read(uint64(i))
		if err == wal.ErrNotFound {
			break
		}
		if err != nil {
			return false, err
		}
		readBytes += uint32(len(eventStreamBytes))
		if err := readerFn(eventStreamBytes); err != nil {
			return false, err
		}
	}
	return truncated, nil
}

// ReadFull reads events from the write ahead log until it reaches it max default size.
func (w *WalLog) ReadFull(readerFn ReaderFunc) (bool, error) {
	return w.ReadAtMost(defaultMaxRead, readerFn)
}
func (w *WalLog) Close() error { return w.wlog.Close() }
