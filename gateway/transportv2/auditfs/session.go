package auditfs

import (
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	pgtypes "github.com/runopsio/hoop/common/pgtypes"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/gateway/apiclient"
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	"github.com/runopsio/hoop/gateway/session/eventlog"
	eventlogv0 "github.com/runopsio/hoop/gateway/session/eventlog/v0"
	sessionwal "github.com/runopsio/hoop/gateway/session/wal"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

var auditStore = memory.New()

type AuditWal struct {
	sessionID string
	walFolder string
	log       *sessionwal.WalLog
	mu        sync.RWMutex
}

// <audit-path>/<orgid>-<sessionid>-wal
const walFolderTmpl string = `%s/%s-%s-wal`

type Options struct {
	OrgID                string
	SessionID            string
	ConnectionType       string
	ConnectionName       string
	Verb                 string
	PostSaveSessionToken string
	StartDate            time.Time
}

func Open(opt Options) error {
	walFolder := fmt.Sprintf(walFolderTmpl, plugintypes.AuditPath, opt.OrgID, opt.SessionID)
	walog, err := sessionwal.OpenWriteHeader(walFolder, &sessionwal.Header{
		OrgID:          opt.OrgID,
		SessionID:      opt.SessionID,
		ConnectionType: opt.ConnectionType,
		ConnectionName: opt.ConnectionName,
		Verb:           opt.Verb,
		StartDate:      &opt.StartDate,
		SessionToken:   opt.PostSaveSessionToken,
	})
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	auditStore.Set(opt.SessionID, &AuditWal{
		sessionID: opt.SessionID,
		walFolder: walFolder,
		log:       walog,
		mu:        sync.RWMutex{},
	})
	return nil
}

func Write(sessionID string, pkt *pb.Packet) error {
	obj := auditStore.Get(sessionID)
	auditWal, ok := obj.(*AuditWal)
	if !ok {
		return fmt.Errorf("failed obtaining wallog for sid=%v, obj=%v", sessionID, auditWal)
	}
	auditWal.mu.Lock()
	defer auditWal.mu.Unlock()
	switch pb.PacketType(pkt.Type) {
	case pbagent.PGConnectionWrite:
		isSimpleQuery, queryBytes, err := pgtypes.SimpleQueryContent(pkt.Payload)
		if !isSimpleQuery {
			break
		}
		if err != nil {
			log.With("sid", sessionID).Errorf("failed parsing simple query data, err=%v", err)
			return fmt.Errorf("failed obtaining simple query data, reason=%v", err)
		}
		return writeLog(auditWal, eventlogv0.InputType, queryBytes, 0)
	case pbagent.MySQLConnectionWrite:
		if queryBytes := decodeMySQLCommandQuery(pkt.Payload); queryBytes != nil {
			return writeLog(auditWal, eventlogv0.InputType, queryBytes, 0)
		}
	case pbclient.WriteStdout,
		pbclient.WriteStderr:
		if err := writeLog(auditWal, eventlogv0.OutputType, pkt.Payload, 0); err != nil {
			log.Warnf("failed writing agent packet response, err=%v", err)
		}
	case pbclient.SessionClose:
		// TODO: must persist the session in this state
		if len(pkt.Payload) > 0 {
			return writeLog(auditWal, eventlogv0.ErrorType, pkt.Payload, 0)
		}
	case pbagent.ExecWriteStdin,
		pbagent.TerminalWriteStdin,
		pbagent.TCPConnectionWrite:
		return writeLog(auditWal, eventlogv0.InputType, pkt.Payload, 0)
	}
	return nil
}

func writeLog(auditWal *AuditWal, eventType eventlogv0.EventType, event []byte, dlpCount uint64) error {
	return auditWal.log.Write(eventlogv0.New(
		time.Now().UTC(),
		eventType,
		dlpCount,
		event))
}

func Close(sessionID string, client *apiclient.Client) error {
	obj := auditStore.Get(sessionID)
	auditWal, ok := obj.(*AuditWal)
	if !ok {
		return fmt.Errorf("failed obtaining wallog for sid=%v, obj=%v", sessionID, auditWal)
	}
	auditWal.mu.Lock()
	defer auditWal.mu.Unlock()
	defer func() {
		auditStore.Del(sessionID)
		_ = auditWal.log.Close()
	}()
	wh, err := auditWal.log.Header()
	if err != nil {
		return fmt.Errorf("failed decoding wal header object, err=%v", err)
	}
	var combinedOutput []byte
	// [[event-stream] [event-stream] ...]
	var eventStreamList []apitypes.SessionEventStream
	eventSize := int64(0)
	redactCount := int64(0)
	truncated, err := auditWal.log.ReadFull(func(data []byte) error {
		ev, err := eventlog.DecodeLatest(data)
		if err != nil {
			return err
		}
		// truncate when event is greater than 5000 bytes for tcp type
		// it avoids auditing blob content for TCP (files, images, etc)
		eventStream := truncateTCPEventStream(ev.Data, wh.ConnectionType)
		switch wh.Verb {
		case pb.ClientVerbExec:
			if ev.EventType != eventlogv0.InputType {
				combinedOutput = append(combinedOutput, eventStream...)
			}
		case pb.ClientVerbConnect:
			eventStreamList = append(eventStreamList, apitypes.SessionEventStream{
				ev.EventTime.Sub(*wh.StartDate).Seconds(),
				string(ev.EventType),
				base64.StdEncoding.EncodeToString(eventStream),
			})
		}
		eventSize += int64(len(ev.Data))
		redactCount += int64(ev.RedactCount)
		return nil
	})
	if err != nil {
		return err
	}
	endDate := time.Now().UTC()
	err = client.CloseSession(sessionID, wh.SessionToken, apitypes.CloseSessionRequest{
		EventStream: eventStreamList,
		EventSize:   eventSize,
		IsTruncated: truncated,
		Output:      string(combinedOutput),
	})
	switch err {
	case nil:
		_ = os.RemoveAll(auditWal.walFolder)
	default:
		_ = auditWal.log.Write(&eventlogv0.EventLog{
			CommitError:   err.Error(),
			CommitEndDate: &endDate,
		})
	}
	return err
}

func truncateTCPEventStream(eventStream []byte, connType string) []byte {
	if len(eventStream) > 5000 && connType == pb.ConnectionTypeTCP {
		return eventStream[0:5000]
	}
	return eventStream
}
