package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/runopsio/hoop/gateway/session"
	pluginscore "github.com/runopsio/hoop/gateway/transport/plugins"
	"github.com/tidwall/wal"
)

const walFolderTmpl string = `%s/%s-%s-wal`

type (
	walHeader struct {
		OrgID          string     `json:"org_id"`
		SessionID      string     `json:"session_id"`
		UserID         string     `json:"user_id"`
		ConnectionName string     `json:"connection_name"`
		ConnectionType string     `json:"connection_type"`
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

func addEventStreamHeader(d time.Time, eventType byte) []byte {
	return append([]byte(d.Format(time.RFC3339Nano)), '\000', eventType, '\000')
}

func parseEventStream(eventStream []byte) (session.EventStream, error) {
	position := bytes.IndexByte(eventStream, '\000')
	if position == -1 {
		return nil, fmt.Errorf("event stream in wrong format [event-time]")
	}
	eventTimeBytes := eventStream[:position]
	eventTime, err := time.Parse(time.RFC3339Nano, string(eventTimeBytes))
	if err != nil {
		return nil, fmt.Errorf("failed parsing event time, err=%v", err)
	}
	position += 2
	if len(eventStream) <= position {
		return nil, fmt.Errorf("event stream in wrong format [event-type]")
	}
	eventType := eventStream[position-1]
	return session.EventStream{eventTime, eventType, eventStream[position:]}, nil
}

func (p *auditPlugin) writeOnConnect(orgID, sessionID, userID, connName, connType string) error {
	walFolder := fmt.Sprintf(walFolderTmpl, pluginAuditPath, orgID, sessionID)
	walog, err := wal.Open(walFolder, wal.DefaultOptions)
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	walHeader, err := encodeWalHeader(&walHeader{
		OrgID:          orgID,
		SessionID:      sessionID,
		UserID:         userID,
		ConnectionName: connName,
		ConnectionType: connType,
		StartDate:      func() *time.Time { d := time.Now().UTC(); return &d }(),
	})
	if err != nil {
		return fmt.Errorf("failed creating wal header object, err=%v", err)
	}
	if err := walog.Write(1, walHeader); err != nil {
		return fmt.Errorf("failed writing header to wal, err=%v", err)
	}
	p.walSessionStore.Set(sessionID, &walLogRWMutex{walog, sync.RWMutex{}, walFolder})
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
	eventHeader := addEventStreamHeader(time.Now().UTC(), eventType)
	if err := walogm.log.Write(lastIndex+1, append(eventHeader, event...)); err != nil {
		return fmt.Errorf("failed writing into wal file, position=%v, err=%v", lastIndex+1, err)
	}
	return nil
}

func (p *auditPlugin) writeOnClose(sessionID string) error {
	walLogObj := p.walSessionStore.Get(sessionID)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		return fmt.Errorf("failed obtaining wallog, obj=%v", walLogObj)
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
	for {
		eventStreamBytes, err := walogm.log.Read(idx)
		if err != nil && err != wal.ErrNotFound {
			return fmt.Errorf("failed reading full session data err=%v", err)
		}
		if err == wal.ErrNotFound {
			break
		}
		eventStream, err := parseEventStream(eventStreamBytes)
		if err != nil {
			return err
		}
		eventStreamList = append(eventStreamList, eventStream)
		idx++
	}
	endDate := time.Now().UTC()
	err = p.storageWriter.Write(pluginscore.ParamsData{
		"org_id":          wh.OrgID,
		"session_id":      wh.SessionID,
		"user_id":         wh.UserID,
		"event_stream":    eventStreamList,
		"connection_name": wh.ConnectionName,
		"connection_type": wh.ConnectionType,
		"start_date":      wh.StartDate,
		"end_time":        &endDate,
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
