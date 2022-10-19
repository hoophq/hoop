package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"time"

	pluginscore "github.com/runopsio/hoop/gateway/plugins/core"
	"github.com/runopsio/hoop/gateway/session"
	"github.com/tidwall/wal"
)

const (
	CommitStatusOpen  string = "open"
	CommitStatusError string = "error"
	CommitStatusOK    string = "ok"
)

type walHeader struct {
	OrgID          string     `json:"org_id"`
	SessionID      string     `json:"session_id"`
	UserID         string     `json:"user_id"`
	ConnectionName string     `json:"connection_name"`
	ConnectionType string     `json:"connection_type"`
	CommitStatus   string     `json:"commit_status"`
	CommitError    string     `json:"commit_error"`
	StartDate      *time.Time `json:"start_date"`
	EndDate        *time.Time `json:"end_date"`
}

func (w *walHeader) validate() error {
	if w.OrgID == "" || w.SessionID == "" ||
		w.CommitStatus == "" || w.ConnectionName == "" ||
		w.ConnectionType == "" || w.StartDate == nil {
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
	return append([]byte(d.Format(time.RFC3339)), '\000', eventType, '\000')
}

func parseEventStream(eventStream []byte) (session.EventStream, error) {
	position := bytes.IndexByte(eventStream, '\000')
	if position == -1 {
		return nil, fmt.Errorf("event stream in wrong format [event-time]")
	}
	eventTimeBytes := eventStream[:position]
	eventTime, err := time.Parse(time.RFC3339, string(eventTimeBytes))
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
	walFile := fmt.Sprintf("%s.%s.wal", orgID, sessionID)
	walog, err := wal.Open(fmt.Sprintf("%s/%s", pluginAuditPath, walFile), wal.DefaultOptions)
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	walHeader, err := encodeWalHeader(&walHeader{
		OrgID:          orgID,
		SessionID:      sessionID,
		UserID:         userID,
		ConnectionName: connName,
		ConnectionType: connType,
		CommitStatus:   CommitStatusOpen,
		StartDate:      func() *time.Time { d := time.Now().UTC(); return &d }(),
	})
	if err != nil {
		return fmt.Errorf("failed creating wal header object, err=%v", err)
	}
	if err := walog.Write(1, walHeader); err != nil {
		return fmt.Errorf("failed writing header to wal, err=%v", err)
	}
	p.walSessionStore.Set(sessionID, walog)
	return nil
}

func (p *auditPlugin) writeOnReceive(sessionID string, eventType byte, event []byte) error {
	walLogObj := p.walSessionStore.Get(sessionID)
	walog, ok := walLogObj.(*wal.Log)
	if !ok {
		return fmt.Errorf("failed obtaining wal log, obj=%v", walLogObj)
	}
	lastIndex, err := walog.LastIndex()
	if err != nil || lastIndex == 0 {
		return fmt.Errorf("failed retrieving wal file content, lastindex=%v, err=%v", lastIndex, err)
	}
	eventHeader := addEventStreamHeader(time.Now().UTC(), eventType)

	if err := walog.Write(lastIndex+1, append(eventHeader, event...)); err != nil {
		return fmt.Errorf("failed writing into wal file, position=%v, err=%v", lastIndex+1, err)
	}
	return nil
}

func (p *auditPlugin) writeOnClose(sessionID string) error {
	walLogObj := p.walSessionStore.Get(sessionID)
	walog, ok := walLogObj.(*wal.Log)
	if !ok {
		return fmt.Errorf("failed obtaining walog, obj=%v", walLogObj)
	}
	defer walog.Close()
	walHeaderData, err := walog.Read(1)
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
		eventStreamBytes, err := walog.Read(idx)
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
	wh.EndDate = &endDate
	wh.CommitStatus = CommitStatusOK
	if err != nil {
		wh.CommitError = err.Error()
		wh.CommitStatus = CommitStatusError
	}
	// add a footer indicating if the log was commited
	walHeaderData, _ = encodeWalHeader(wh)
	if err := walog.Write(idx, walHeaderData); err != nil {
		log.Printf("failed writing wal footer, err=%v", err)
	}
	return err
}
