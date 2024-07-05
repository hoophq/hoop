package index

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/indexer"
	"github.com/hoophq/hoop/gateway/session/eventlog"
	eventlogv0 "github.com/hoophq/hoop/gateway/session/eventlog/v0"
	sessionwal "github.com/hoophq/hoop/gateway/session/wal"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var walFolderTmpl string = `%s/%s-%s-wal`

type walLogRWMutex struct {
	wlog            *sessionwal.WalLog
	mu              sync.RWMutex
	folderName      string
	stdinSize       int64
	stdoutSize      int64
	stdinTruncated  bool
	stdoutTruncated bool
}

func (p *indexPlugin) writeOnConnect(c plugintypes.Context) error {
	walFolder := fmt.Sprintf(walFolderTmpl, plugintypes.IndexPath, c.OrgID, c.SID)
	// sometimes a client could execute the same session id (review flow bug)
	if fi, _ := os.Stat(walFolder); fi != nil {
		_ = os.RemoveAll(walFolder)
	}
	walog, err := sessionwal.OpenWriteHeader(walFolder, &sessionwal.Header{
		OrgID:          c.OrgID,
		SessionID:      c.SID,
		UserEmail:      c.UserEmail,
		ConnectionName: c.ConnectionName,
		ConnectionType: c.ConnectionType,
		Verb:           c.ClientVerb,
		StartDate:      func() *time.Time { t := time.Now().UTC(); return &t }(),
	})
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	p.walSessionStore.Set(c.SID, &walLogRWMutex{
		wlog:       walog,
		mu:         sync.RWMutex{},
		folderName: walFolder,
	})
	return nil
}

func (p *indexPlugin) writeOnReceive(sid string, eventType eventlogv0.EventType, event []byte) error {
	walLogObj := p.walSessionStore.Get(sid)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		log.With("sid", sid).Warnf("failed obtaining wallog, obj=%v", walLogObj)
		return nil
	}
	walogm.mu.Lock()
	defer walogm.mu.Unlock()

	eventSize := int64(len(event))

	switch eventType {
	case eventlogv0.InputType:
		if walogm.stdinSize >= indexer.MaxIndexSize {
			walogm.stdinTruncated = true
			return nil
		}
		walogm.stdinSize += eventSize
	case eventlogv0.OutputType, eventlogv0.ErrorType:
		if walogm.stdoutSize >= indexer.MaxIndexSize {
			walogm.stdoutTruncated = true
			return nil
		}
		walogm.stdoutSize += eventSize
	}
	err := walogm.wlog.Write(eventlogv0.New(time.Now().UTC(), eventType, 0, event))
	if err != nil {
		log.With("sid", sid).Warnf("failed writing into wal file, err=%v", err)
		return nil
	}
	return nil
}

func (p *indexPlugin) indexOnClose(c plugintypes.Context, isError bool) {
	walLogObj := p.walSessionStore.Get(c.SID)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		log.With("sid", c.SID).Infof("wal log not found")
		return
	}
	walogm.mu.Lock()
	defer func() {
		p.walSessionStore.Del(c.SID)
		_ = walogm.wlog.Close()
		walogm.mu.Unlock()
		_ = os.RemoveAll(walogm.folderName)
	}()

	wh, err := walogm.wlog.Header()
	if err != nil {
		log.With("sid", c.SID).Infof("failed decoding wal header object, err=%v", err)
		return
	}

	var stdinData []byte
	var stdoutData []byte
	_, err = walogm.wlog.ReadFull(func(data []byte) error {
		ev, err := eventlog.DecodeLatest(data)
		if err != nil {
			return err
		}
		switch ev.EventType {
		case eventlogv0.InputType:
			stdinData = append(stdinData, ev.Data...)
		case eventlogv0.ErrorType, eventlogv0.OutputType:
			stdoutData = append(stdoutData, ev.Data...)
		}
		return nil
	})
	if err != nil {
		log.With("sid", c.SID).Errorf("indexed=false, failed reading event log", c.SID)
		return
	}

	endDate := time.Now().UTC()
	durationInSecs := int64(endDate.Sub(*wh.StartDate).Seconds())
	payload := &indexer.Session{
		OrgID:             c.OrgID,
		ID:                c.SID,
		User:              wh.UserEmail,
		Connection:        wh.ConnectionName,
		ConnectionType:    wh.ConnectionType,
		Verb:              wh.Verb,
		EventSize:         int64(len(stdinData) + len(stdoutData)),
		Input:             string(stdinData),
		Output:            string(stdoutData),
		IsInputTruncated:  walogm.stdinTruncated,
		IsOutputTruncated: walogm.stdoutTruncated,
		IsError:           isError,
		StartDate:         wh.StartDate.Format(time.RFC3339),
		EndDate:           endDate.Format(time.RFC3339),
		Duration:          durationInSecs,
	}
	indexCh := p.indexers.Get(c.OrgID).(chan *indexer.Session)
	if indexCh == nil {
		log.With("sid", c.SID).Infof("indexed=false, channel not found in memory")
	}
	select {
	case indexCh <- payload:
	default:
	case <-time.After(2 * time.Second):
		log.With("sid", c.SID).Infof("indexed=false, timeout on sending to channel", c.SID)
	}
}
