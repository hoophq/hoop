package audit

import (
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/session/eventlog"
	eventlogv0 "github.com/runopsio/hoop/gateway/session/eventlog/v0"
	sessionwal "github.com/runopsio/hoop/gateway/session/wal"
	"github.com/runopsio/hoop/gateway/storagev2"
	sessionstorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

// <audit-path>/<orgid>-<sessionid>-wal
const walFolderTmpl string = `%s/%s-%s-wal`

type walLogRWMutex struct {
	log        *sessionwal.WalLog
	mu         sync.RWMutex
	folderName string
}

func (p *auditPlugin) writeOnConnect(pctx plugintypes.Context) error {
	walFolder := fmt.Sprintf(walFolderTmpl, plugintypes.AuditPath, pctx.OrgID, pctx.SID)
	// sometimes a client could execute the same session id (review flow bug)
	if fi, _ := os.Stat(walFolder); fi != nil {
		_ = os.RemoveAll(walFolder)
	}

	walog, err := sessionwal.OpenWriteHeader(walFolder, &sessionwal.Header{
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
		StartDate:      pctx.ParamsData.GetTime("start_date"),
	})
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	p.walSessionStore.Set(pctx.SID, &walLogRWMutex{walog, sync.RWMutex{}, walFolder})
	return nil
}

func (p *auditPlugin) writeOnReceive(sessionID string, eventType eventlogv0.EventType, dlpCount int64, event []byte) error {
	walLogObj := p.walSessionStore.Get(sessionID)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		return fmt.Errorf("failed obtaining wallog for sid=%v, obj=%v", sessionID, walLogObj)
	}
	walogm.mu.Lock()
	defer walogm.mu.Unlock()
	return walogm.log.Write(eventlogv0.New(
		time.Now().UTC(),
		eventType,
		uint64(dlpCount),
		event))
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
	wh, err := walogm.log.Header()
	if err != nil {
		return fmt.Errorf("failed decoding wal header object, err=%v", err)
	}
	if wh.SessionID != sessionID {
		return fmt.Errorf("mismatch wal header session id, session=%v, session-header=%v",
			sessionID, wh.SessionID)
	}
	var eventStreamList []types.SessionEventStream
	eventSize := int64(0)
	redactCount := int64(0)
	truncated, err := walogm.log.ReadFull(func(data []byte) error {
		ev, err := eventlog.DecodeLatest(data)
		if err != nil {
			return err
		}
		eventSize += int64(len(ev.Data))
		redactCount += int64(ev.RedactCount)
		// don't process empty event streams
		if len(ev.Data) == 0 {
			return nil
		}

		// truncate when event is greater than 5000 bytes for tcp type
		// it avoids auditing blob content for TCP (files, images, etc)
		eventStream := p.truncateTCPEventStream(ev.Data, wh.ConnectionType)
		eventStreamList = append(eventStreamList, types.SessionEventStream{
			ev.EventTime.Sub(*wh.StartDate).Seconds(),
			string(ev.EventType),
			base64.StdEncoding.EncodeToString(eventStream),
		})
		return nil
	})
	if err != nil {
		return err
	}

	storageContext := storagev2.NewContext(wh.UserID, wh.OrgID, storagev2.NewStorage(nil))
	session, err := sessionstorage.FindOne(storageContext, wh.SessionID)
	if err != nil || session == nil {
		return fmt.Errorf("fail to fetch session in the store, empty=%v, err=%v",
			session == nil, err)
	}
	endDate := time.Now().UTC()
	labels := map[string]string{}
	var inputScript types.SessionScript
	var metadata map[string]any
	if session != nil {
		inputScript = session.Script
		for key, val := range session.Labels {
			labels[key] = val
		}
		metadata = session.Metadata
	}
	labels["processed-by"] = "plugin-audit"
	labels["truncated"] = fmt.Sprintf("%v", truncated)
	pgsession.New().Upsert(storageContext, types.Session{
		ID:               wh.SessionID,
		OrgID:            wh.OrgID,
		UserEmail:        wh.UserEmail,
		UserID:           wh.UserID,
		UserName:         wh.UserName,
		Type:             wh.ConnectionType,
		Connection:       wh.ConnectionName,
		Verb:             wh.Verb,
		Status:           types.SessionStatusDone,
		Script:           inputScript,
		Labels:           labels,
		Metadata:         metadata,
		NonIndexedStream: types.SessionNonIndexedEventStreamList{"stream": eventStreamList},
		EventSize:        eventSize,
		StartSession:     *wh.StartDate,
		EndSession:       &endDate,
		DlpCount:         redactCount,
	})

	if err != nil {
		if err := walogm.log.Write(&eventlogv0.EventLog{
			CommitError:   err.Error(),
			CommitEndDate: &endDate,
		}); err != nil {
			log.Warnf("failed writing wal footer, err=%v", err)
		}
	} else {
		if err := os.RemoveAll(walogm.folderName); err != nil {
			log.Printf("failed removing wal file %q, err=%v", walogm.folderName, err)
		}
	}
	return err
}

func (p *auditPlugin) truncateTCPEventStream(eventStream []byte, connType string) []byte {
	if len(eventStream) > 5000 && connType == pb.ConnectionTypeTCP.String() {
		return eventStream[0:5000]
	}
	return eventStream
}
