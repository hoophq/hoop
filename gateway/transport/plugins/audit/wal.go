package audit

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2"
	sessionStorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/tidwall/wal"

	"github.com/runopsio/hoop/common/log"
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
		Status         string     `json:"status"`
		Script         string     `json:"script"`
		Labels         string     `json:"labels"` // we save it as string and convert at storage layer
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

func parseEventStream(eventStream []byte) (types.SessionEventStream, int, int64, error) {
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
	position += 9
	if len(eventStream) <= position {
		return nil, -1, 0, fmt.Errorf("event stream in wrong format [event-type]")
	}
	dlpCounter := byteArrayToInt(eventStream[position-8 : position])

	eventStreamLength := len(eventStream[position:])
	return types.SessionEventStream{eventTime, eventType, eventStream[position:]},
		eventStreamLength, dlpCounter, nil
}

func (p *auditPlugin) writeOnConnect(pctx plugintypes.Context) error {
	walFolder := fmt.Sprintf(walFolderTmpl, plugintypes.AuditPath, pctx.OrgID, pctx.SID)
	_ = os.RemoveAll(walFolder)
	walog, err := wal.Open(walFolder, wal.DefaultOptions)
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}

	walHeader, err := encodeWalHeader(&walHeader{
		OrgID:          pctx.OrgID,
		SessionID:      pctx.SID,
		UserID:         pctx.UserID,
		UserName:       pctx.UserName,
		UserEmail:      pctx.UserEmail,
		ConnectionName: pctx.ConnectionName,
		ConnectionType: pctx.ConnectionType,
		Verb:           pctx.ClientVerb,
		Script:         pctx.ParamsData.GetString("script"),
		Labels:         pctx.ParamsData.GetString("labels"),
		Status:         pctx.ParamsData.GetString("status"),
		StartDate:      func() *time.Time { d := time.Now().UTC(); return &d }(),
	})
	if err != nil {
		return fmt.Errorf("failed creating wal header object, err=%v", err)
	}
	if err := walog.Write(1, walHeader); err != nil {
		return fmt.Errorf("failed writing header to wal, err=%v", err)
	}
	p.walSessionStore.Set(pctx.SID, &walLogRWMutex{walog, sync.RWMutex{}, walFolder})
	return nil
}

func (p *auditPlugin) writeOnReceive(sessionID string, eventType byte, dlpCount int64, event []byte) error {
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
	eventHeader := addEventStreamHeader(time.Now().UTC(), eventType, dlpCount)
	if err := walogm.log.Write(lastIndex+1, append(eventHeader, event...)); err != nil {
		return fmt.Errorf("failed writing into wal file, position=%v, err=%v", lastIndex+1, err)
	}
	return nil
}

func (p *auditPlugin) writeOnClose(pctx plugintypes.Context) error {
	sessionID := pctx.SID
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
	var eventStreamList []types.SessionEventStream
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

	newStorage := storagev2.NewStorage(nil)
	storageContext := storagev2.NewContext(wh.UserID, wh.OrgID, newStorage)
	session, err := sessionStorage.FindOne(storageContext, wh.SessionID)
	if err != nil || session == nil {
		return err
	}

	pluginctx := plugintypes.Context{
		OrgID:          wh.OrgID,
		SID:            wh.SessionID,
		UserID:         wh.UserID,
		UserName:       wh.UserName,
		UserEmail:      wh.UserEmail,
		ConnectionName: wh.ConnectionName,
		ConnectionType: wh.ConnectionType,
		Script:         session.Script["data"],
		Labels:         session.Labels,
		ClientVerb:     wh.Verb,
		ParamsData: map[string]any{
			"event_stream": eventStreamList,
			"event_size":   eventSize,
			"start_date":   wh.StartDate,
			"status":       "done",
			"end_time":     &endDate,
			"dlp_count":    dlpCounter,
		},
	}
	err = p.storageWriter.Write(pluginctx)
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
	var b [8]byte
	s := b[:]
	binary.BigEndian.PutUint64(s, uint64(i))
	return s
}

func byteArrayToInt(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b))
}
