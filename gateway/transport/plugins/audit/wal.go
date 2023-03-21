package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"reflect"
	"sync"
	"time"
	"unsafe"

	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/session"
	"github.com/tidwall/wal"
)

const walFolderTmpl string = `%s/%s-%s-wal`

type (
	walHeader struct {
		OrgID          string     `json:"org_id"`
		SessionID      string     `json:"session_id"`
		UserID         string     `json:"user_id"`
		UserName       string     `json:"user_name"`
		UserEmail      string     `json:"user_email"`
		ConnectionName string     `json:"connection_name"`
		ConnectionType string     `json:"connection_type"`
		Verb           string     `json:"verb"`
		StartDate      *time.Time `json:"start_date"`
	}
	walFooter struct {
		CommitError string     `json:"commit_error"`
		EndDate     *time.Time `json:"end_date"`
	}
	walLogRWMutex struct {
		log        *wal.Log
		mu         sync.RWMutex
		folderName string
	}
)

func (w *walHeader) validate() error {
	if w.OrgID == "" || w.SessionID == "" ||
		w.ConnectionType == "" || w.ConnectionName == "" ||
		w.StartDate == nil {
		return fmt.Errorf(`missing required values for wal session`)
	}
	return nil
}

func encodeWalHeader(w *walHeader) ([]byte, error) {
	if err := w.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

func decodeWalHeader(data []byte) (*walHeader, error) {
	var ws walHeader
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, err
	}
	return &ws, ws.validate()
}

func addEventStreamHeader(d time.Time, eventType byte, dlpCount int64) []byte {
	result := append([]byte(d.Format(time.RFC3339Nano)), '\000', eventType, '\000')
	result = append(result, intToByteArray(dlpCount)...) // int64 uses a fixed 8 bytes
	return append(result, '\000')
}

func parseEventStream(eventStream []byte) (session.EventStream, int, int64, error) {
	position := bytes.IndexByte(eventStream, '\000')
	if position == -1 {
		return nil, -1, 0, fmt.Errorf("event stream in wrong format [event-time]")
	}
	eventTimeBytes := eventStream[:position]
	eventTime, err := time.Parse(time.RFC3339Nano, string(eventTimeBytes))
	if err != nil {
		return nil, -1, 0, fmt.Errorf("failed parsing event time, err=%v", err)
	}
	position += 2
	if len(eventStream) <= position {
		return nil, -1, 0, fmt.Errorf("event stream in wrong format [event-type]")
	}
	eventType := eventStream[position-1]

	// dlp counter uses 8-byte (int64)
	position = bytes.LastIndexByte(eventStream, '\000')
	if position == -1 {
		return nil, -1, 0, fmt.Errorf("event stream in wrong format [dlp-count]")
	}
	eventDlpCounter := eventStream[position-8 : position]
	dlpCounter := byteArrayToInt(eventDlpCounter)

	eventStreamLength := len(eventStream[position:])
	return session.EventStream{eventTime, eventType, eventStream[position:]},
		eventStreamLength, dlpCounter, nil
}

func (p *auditPlugin) writeOnConnect(config plugin.Config) error {
	walFolder := fmt.Sprintf(walFolderTmpl, pluginAuditPath, config.Org, config.SessionId)
	walog, err := wal.Open(walFolder, wal.DefaultOptions)
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	walHeader, err := encodeWalHeader(&walHeader{
		OrgID:          config.Org,
		SessionID:      config.SessionId,
		UserID:         config.UserID,
		UserName:       config.UserName,
		UserEmail:      config.UserEmail,
		ConnectionName: config.ConnectionName,
		ConnectionType: config.ConnectionType,
		Verb:           config.Verb,
		StartDate:      func() *time.Time { d := time.Now().UTC(); return &d }(),
	})
	if err != nil {
		return fmt.Errorf("failed creating wal header object, err=%v", err)
	}
	if err := walog.Write(1, walHeader); err != nil {
		return fmt.Errorf("failed writing header to wal, err=%v", err)
	}
	p.walSessionStore.Set(config.SessionId, &walLogRWMutex{walog, sync.RWMutex{}, walFolder})
	return nil
}

func (p *auditPlugin) writeOnReceive(sessionID string, eventType byte, event []byte) error {
	walLogObj := p.walSessionStore.Get(sessionID)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		return fmt.Errorf("failed obtaining wallog, obj=%v", walLogObj)
	}
	walogm.mu.Lock()
	defer walogm.mu.Unlock()
	lastIndex, err := walogm.log.LastIndex()
	if err != nil || lastIndex == 0 {
		return fmt.Errorf("failed retrieving wal file content, lastindex=%v, err=%v", lastIndex, err)
	}
	dlpCount := int64(20) // get dlp count
	eventHeader := addEventStreamHeader(time.Now().UTC(), eventType, dlpCount)
	if err := walogm.log.Write(lastIndex+1, append(eventHeader, event...)); err != nil {
		return fmt.Errorf("failed writing into wal file, position=%v, err=%v", lastIndex+1, err)
	}
	return nil
}

func (p *auditPlugin) writeOnClose(sessionID string) error {
	walLogObj := p.walSessionStore.Get(sessionID)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		return nil
	}
	walogm.mu.Lock()
	defer func() {
		p.walSessionStore.Del(sessionID)
		_ = walogm.log.Close()
		walogm.mu.Unlock()
	}()
	walHeaderData, err := walogm.log.Read(1)
	if err != nil {
		return fmt.Errorf("failed obtaining header from wal, err=%v", err)
	}
	wh, err := decodeWalHeader(walHeaderData)
	if err != nil {
		return fmt.Errorf("failed decoding wal header object, err=%v", err)
	}
	if wh.SessionID != sessionID {
		return fmt.Errorf("mismatch wal header session id, session=%v, session-header=%v",
			sessionID, wh.SessionID)
	}
	idx := uint64(2)
	var eventStreamList []session.EventStream
	eventSize := int64(0)
	dlpCounter := int64(0)
	for {
		eventStreamBytes, err := walogm.log.Read(idx)
		if err != nil && err != wal.ErrNotFound {
			return fmt.Errorf("failed reading full session data err=%v", err)
		}
		// truncate when event is greater than 5000 bytes for tcp type
		// it avoids auditing blob content for TCP (files, images, etc)
		eventStreamBytes = p.truncateTCPEventStream(eventStreamBytes, wh.ConnectionType)
		if err == wal.ErrNotFound {
			break
		}
		eventStream, size, dlpCount, err := parseEventStream(eventStreamBytes)
		if err != nil {
			return err
		}
		eventStreamList = append(eventStreamList, eventStream)
		eventSize += int64(size)
		dlpCounter += dlpCount
		idx++
	}
	endDate := time.Now().UTC()
	err = p.storageWriter.Write(plugin.Config{
		Org:            wh.OrgID,
		SessionId:      wh.SessionID,
		UserID:         wh.UserID,
		UserName:       wh.UserName,
		UserEmail:      wh.UserEmail,
		ConnectionName: wh.ConnectionName,
		ConnectionType: wh.ConnectionType,
		Verb:           wh.Verb,
		ParamsData: map[string]any{
			"event_stream": eventStreamList,
			"event_size":   eventSize,
			"start_date":   wh.StartDate,
			"end_time":     &endDate,
			"dlp_count":    dlpCounter,
		},
	})
	if err != nil {
		walFooterBytes, _ := json.Marshal(&walFooter{
			CommitError: err.Error(),
			EndDate:     &endDate,
		})
		if err := walogm.log.Write(idx, walFooterBytes); err != nil {
			log.Printf("failed writing wal footer, err=%v", err)
		}
	} else {
		if err := os.RemoveAll(walogm.folderName); err != nil {
			log.Printf("failed removing wal file %q, err=%v", walogm.folderName, err)
		}
	}
	return err
}

func (p *auditPlugin) truncateTCPEventStream(eventStream []byte, connType string) []byte {
	if len(eventStream) > 5000 && connType == pb.ConnectionTypeTCP {
		return eventStream[0:5000]
	}
	return eventStream
}

func intToByteArray(i int64) []byte {
	var b []byte
	sh := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	sh.Len = 8
	sh.Cap = 8
	sh.Data = uintptr(unsafe.Pointer(&i))

	return b[:]
}

func byteArrayToInt(b []byte) int64 {
	return *(*int64)(unsafe.Pointer(&b[0]))
}
