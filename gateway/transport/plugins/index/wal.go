package index

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/tidwall/wal"
)

var walFolderTmpl string = `%s/%s-%s-wal`

type walLogRWMutex struct {
	log             *wal.Log
	mu              sync.RWMutex
	folderName      string
	stdinSize       int64
	stdoutSize      int64
	stdinTruncated  bool
	stdoutTruncated bool
	metadata        *walMetadata
}

type walMetadata struct {
	OrgID          string    `json:"org_id"`
	SessionID      string    `json:"session_id"`
	UserEmail      string    `json:"user_email"`
	ConnectionName string    `json:"connection_name"`
	ConnectionType string    `json:"connection_type"`
	Verb           string    `json:"verb"`
	StartDate      time.Time `json:"start_date"`
}

func (p *indexPlugin) writeOnConnect(c plugin.Config) error {
	walFolder := fmt.Sprintf(walFolderTmpl, plugin.IndexPath, c.Org, c.SessionId)
	walog, err := wal.Open(walFolder, wal.DefaultOptions)
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	p.walSessionStore.Set(c.SessionId, &walLogRWMutex{
		log:        walog,
		mu:         sync.RWMutex{},
		folderName: walFolder,
		metadata: &walMetadata{
			OrgID:          c.Org,
			SessionID:      c.SessionId,
			UserEmail:      c.UserEmail,
			ConnectionName: c.ConnectionName,
			ConnectionType: c.ConnectionType,
			Verb:           c.Verb,
			StartDate:      time.Now().UTC(),
		},
	})
	return nil
}

func (p *indexPlugin) writeOnReceive(sessionID string, eventType string, event []byte) error {
	walLogObj := p.walSessionStore.Get(sessionID)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		return fmt.Errorf("failed obtaining wallog, obj=%v", walLogObj)
	}
	walogm.mu.Lock()
	defer walogm.mu.Unlock()

	lastIndex, err := walogm.log.LastIndex()
	if err != nil {
		return fmt.Errorf("failed retrieving wal file content, lastindex=%v, err=%v", lastIndex, err)
	}
	event = append([]byte(eventType), event...)
	eventSize := int64(len(event))

	switch eventType {
	case "i":
		if walogm.stdinSize >= indexer.MaxIndexSize {
			walogm.stdinTruncated = true
			return nil
		}
		walogm.stdinSize += eventSize
	case "e", "o":
		if walogm.stdoutSize >= indexer.MaxIndexSize {
			walogm.stdoutTruncated = true
			return nil
		}
		walogm.stdoutSize += eventSize
	}
	if err := walogm.log.Write(lastIndex+1, event); err != nil {
		return fmt.Errorf("failed writing into wal file, position=%v, err=%v", lastIndex+1, err)
	}
	return nil
}

func (p *indexPlugin) indexOnClose(c plugin.Config, isError bool) {
	walLogObj := p.walSessionStore.Get(c.SessionId)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		log.Printf("session=%v - wal log not found", c.SessionId)
		return
	}
	walogm.mu.Lock()
	defer func() {
		p.walSessionStore.Del(c.SessionId)
		_ = walogm.log.Close()
		walogm.mu.Unlock()
		_ = os.RemoveAll(walogm.folderName)
	}()
	idx := uint64(2)
	var stdinData []byte
	var stdoutData []byte
	for {
		eventBytes, err := walogm.log.Read(idx)
		if err != nil && err != wal.ErrNotFound {
			log.Printf("session=%v - failed reading full session data err=%v", c.SessionId, err)
			return
		}
		if err == wal.ErrNotFound {
			break
		}

		eventType := eventBytes[0]
		switch eventType {
		case 'i':
			stdinData = append(stdinData, eventBytes[1:]...)
		case 'o', 'e':
			stdoutData = append(stdoutData, eventBytes[1:]...)
		}
		idx++
	}
	endDate := time.Now().UTC()
	durationInSecs := int64(endDate.Sub(walogm.metadata.StartDate).Seconds())
	payload := &indexer.Session{
		OrgID:             c.Org,
		ID:                c.SessionId,
		User:              walogm.metadata.UserEmail,
		Connection:        walogm.metadata.ConnectionName,
		ConnectionType:    walogm.metadata.ConnectionType,
		Verb:              walogm.metadata.Verb,
		EventSize:         int64(len(stdinData) + len(stdoutData)),
		Input:             string(stdinData),
		Output:            string(stdoutData),
		IsInputTruncated:  walogm.stdinTruncated,
		IsOutputTruncated: walogm.stdoutTruncated,
		IsError:           isError,
		StartDate:         walogm.metadata.StartDate.Format(time.RFC3339),
		EndDate:           endDate.Format(time.RFC3339),
		Duration:          durationInSecs,
	}
	indexCh := p.indexers.Get(c.Org).(chan *indexer.Session)
	if indexCh == nil {
		log.Printf("session=%v - indexed=false, channel not found in memory", c.SessionId)
	}
	select {
	case indexCh <- payload:
	default:
	case <-time.After(2 * time.Second):
		log.Printf("session=%v - indexed=false, timeout on sending to channel", c.SessionId)
	}
}
