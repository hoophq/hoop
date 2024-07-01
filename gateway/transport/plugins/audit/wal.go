package audit

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/proto/spectypes"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	eventlogv1 "github.com/runopsio/hoop/gateway/session/eventlog/v1"
	sessionwal "github.com/runopsio/hoop/gateway/session/wal"
	"github.com/runopsio/hoop/gateway/storagev2"
	sessionstorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

// <audit-path>/<orgid>-<sessionid>-wal
const (
	walFolderTmpl    string = `%s/%s-%s-wal`
	eventLogTypeName string = "_footer_error"
)

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
		EventLogVersion: eventlogv1.Version,
		OrgID:           pctx.OrgID,
		SessionID:       pctx.SID,
		UserID:          pctx.UserID,
		UserName:        pctx.UserName,
		UserEmail:       pctx.UserEmail,
		ConnectionName:  pctx.ConnectionName,
		ConnectionType:  pctx.ConnectionType,
		Verb:            pctx.ClientVerb,
		Script:          pctx.ParamsData.GetString("script"),
		Labels:          pctx.ParamsData.GetString("labels"),
		Status:          pctx.ParamsData.GetString("status"),
		StartDate:       pctx.ParamsData.GetTime("start_date"),
	})
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	p.walSessionStore.Set(pctx.SID, &walLogRWMutex{walog, sync.RWMutex{}, walFolder})
	return nil
}

func (p *auditPlugin) writeOnReceive(sessionID string, eventType eventlogv1.EventType, event []byte, metadata map[string][]byte) error {
	walLogObj := p.walSessionStore.Get(sessionID)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		return fmt.Errorf("failed obtaining write ahead log for session %v", sessionID)
	}
	walogm.mu.Lock()
	defer walogm.mu.Unlock()
	return walogm.log.Write(eventlogv1.New(time.Now().UTC(), eventType, event, metadata))
}

func (p *auditPlugin) writeOnClose(pctx plugintypes.Context, errMsg error) error {
	walLogObj := p.walSessionStore.Pop(pctx.SID)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		return nil
	}
	walogm.mu.Lock()
	defer func() { _ = walogm.log.Close(); walogm.mu.Unlock() }()
	// we could add an attribute to have the last message
	// propagated as metadata instead inside the stream
	if errMsg != nil && errMsg != io.EOF {
		err := walogm.log.Write(eventlogv1.New(time.Now().UTC(), eventlogv1.ErrorType, []byte(errMsg.Error()), nil))
		if err != nil {
			log.With("sid", pctx.SID).Warnf("failed writing end error message, err=%v", err)
		}
	}
	wh, err := walogm.log.Header()
	if err != nil {
		return fmt.Errorf("failed decoding wal header object, err=%v", err)
	}
	if wh.SessionID != pctx.SID {
		return fmt.Errorf("mismatch wal header session id, session=%v, session-header=%v",
			pctx.SID, wh.SessionID)
	}
	var eventStreamList []types.SessionEventStream
	metrics := newSessionMetric()
	truncated, err := walogm.log.ReadFull(func(data []byte) error {
		ev, err := eventlogv1.Decode(data)
		if err != nil {
			return err
		}
		metrics.EventSize += int64(len(ev.Payload))
		if infoEnc := ev.GetMetadata(spectypes.DataMaskingInfoKey); infoEnc != nil {
			dataMaskingInfo, err := spectypes.Decode(infoEnc)
			if err != nil {
				log.Warnf("failed decoding data masking info, reason=%v", err)
				dataMaskingInfo = &spectypes.DataMaskingInfo{}
			}
			for _, item := range dataMaskingInfo.Items {
				// it could be decoded with nil items
				if item == nil {
					continue
				}
				metrics.DataMasking.TransformedBytes += item.TransformedBytes
				if item.Err != nil {
					metrics.DataMasking.ErrCount++
					continue
				}

				for _, ts := range item.Summaries {
					var redactPerInfoType int64
					for _, rs := range ts.Results {
						switch rs.Code {
						case "SUCCESS":
							metrics.DataMasking.TotalRedactCount += int64(rs.Count)
							redactPerInfoType += int64(rs.Count)
						case "ERROR":
							metrics.DataMasking.ErrCount++
						}
					}
					if ts.InfoType == "" {
						// ignore it, this should only happened
						// for legacy transformation summary records
						continue
					}
					metrics.addInfoType(ts.InfoType, redactPerInfoType)
				}
			}
		}

		// don't process empty event streams
		if len(ev.Payload) == 0 {
			return nil
		}

		// truncate when event is greater than 5000 bytes for tcp type
		// it avoids auditing blob content for TCP (files, images, etc)
		eventStream := p.truncateTCPEventStream(ev.Payload, wh.ConnectionType)
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

	storageContext := storagev2.NewContext(wh.UserID, wh.OrgID)
	session, err := sessionstorage.FindOne(storageContext, wh.SessionID)
	if err != nil || session == nil {
		return fmt.Errorf("fail to fetch session in the store, empty=%v, err=%v",
			session == nil, err)
	}
	endDate := time.Now().UTC()
	if len(session.Labels) == 0 {
		session.Labels = map[string]string{}
	}
	session.Metrics, err = metrics.toMap()
	if err != nil {
		log.Warnf("failed parsing session metrics to map, reason=%v", err)
	}
	session.Labels["processed-by"] = "plugin-audit"
	session.Labels["truncated"] = fmt.Sprintf("%v", truncated)
	err = pgsession.New().Upsert(storageContext, types.Session{
		ID:               wh.SessionID,
		OrgID:            wh.OrgID,
		UserEmail:        wh.UserEmail,
		UserID:           wh.UserID,
		UserName:         wh.UserName,
		Type:             wh.ConnectionType,
		Connection:       wh.ConnectionName,
		Verb:             wh.Verb,
		Status:           types.SessionStatusDone,
		Script:           session.Script,
		Labels:           session.Labels,
		Metadata:         session.Metadata,
		Metrics:          session.Metrics,
		NonIndexedStream: types.SessionNonIndexedEventStreamList{"stream": eventStreamList},
		EventSize:        metrics.EventSize,
		StartSession:     *wh.StartDate,
		EndSession:       &endDate,
	})

	if err != nil {
		_ = walogm.log.Write(eventlogv1.NewCommitError(endDate, err.Error()))
	} else {
		if err := os.RemoveAll(walogm.folderName); err != nil {
			log.Errorf("failed removing wal file %q, err=%v", walogm.folderName, err)
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
