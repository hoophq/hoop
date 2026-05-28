package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/common/proto/spectypes"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/session/eventbroker"
	eventlogv1 "github.com/hoophq/hoop/gateway/session/eventlog/v1"
	sessionwal "github.com/hoophq/hoop/gateway/session/wal"
	"github.com/hoophq/hoop/gateway/session/wal"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// <audit-path>/<orgid>-<sessionid>-wal
const walFolderTmpl string = `%s/%s-%s-wal`

// machineFlushInterval is how often a machine session's WAL tail is flushed
// to the session's blob_stream during the session.
const machineFlushInterval = 50 * time.Second

var internalExitCode = func() *int { v := 254; return &v }()

type walLogRWMutex struct {
	log        *sessionwal.WalLog
	mu         sync.RWMutex
	folderName string

	// machine-session-only fields
	lastFlushedIndex uint64
	bytesFlushed     int64
	flushCancel      context.CancelFunc
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
		Status:          pctx.ParamsData.GetString("status"),
		StartDate:       pctx.ParamsData.GetTime("start_date"),
	})
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	p.walSessionStore.Set(pctx.SID, &walLogRWMutex{log: walog, folderName: walFolder})
	return nil
}

// startMachineFlushTicker creates the empty stream blob and spawns a goroutine
// that flushes new WAL entries to blob_stream every machineFlushInterval.
// The flush goroutine stops when the returned cancel func is invoked (typically
// at OnDisconnect, which then performs a final flush).
func (p *auditPlugin) startMachineFlushTicker(pctx plugintypes.Context) error {
	walogm, ok := p.walSessionStore.Get(pctx.SID).(*walLogRWMutex)
	if !ok {
		return fmt.Errorf("failed obtaining wal for session %v", pctx.SID)
	}

	if err := models.CreateEmptySessionStreamBlob(pctx.OrgID, pctx.SID, blobFormatFor(pctx.ProtoConnectionType())); err != nil {
		return fmt.Errorf("failed creating empty stream blob: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	walogm.flushCancel = cancel

	go func() {
		ticker := time.NewTicker(machineFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.flushMachineSession(walogm, pctx); err != nil {
					log.With("sid", pctx.SID).Warnf("scheduled flush failed: %v", err)
				}
			}
		}
	}()
	return nil
}

func (p *auditPlugin) writeOnReceive(pctx plugintypes.Context, eventType eventlogv1.EventType, event []byte, metadata map[string][]byte) error {
	walogm, ok := p.walSessionStore.Get(pctx.SID).(*walLogRWMutex)
	if !ok {
		return fmt.Errorf("failed obtaining write ahead log for session %v", pctx.SID)
	}
	walogm.mu.Lock()
	now := time.Now().UTC()
	err := walogm.log.Write(eventlogv1.New(now, eventType, event, metadata))
	walogm.mu.Unlock()
	if err != nil {
		return err
	}

	// machine sessions feed the live SSE broker as events arrive
	if pctx.IdentityType == identityTypeMachine {
		eventbroker.Default.Publish(pctx.SID, eventbroker.Event{
			Time:    now,
			Type:    string(eventType),
			Payload: event,
		})
	}
	return nil
}

func (p *auditPlugin) dropWalLog(sid string) {
	walogm, ok := p.walSessionStore.Pop(sid).(*walLogRWMutex)
	if !ok {
		return
	}
	// cancel before taking the lock so an in-flight tick that already passed
	// the select observes ctx.Done() before contending for the WAL
	if walogm.flushCancel != nil {
		walogm.flushCancel()
	}
	walogm.mu.Lock()
	_ = walogm.log.Close()
	if err := os.RemoveAll(walogm.folderName); err != nil {
		log.Errorf("failed removing wal file %q, err=%v", walogm.folderName, err)
	}
	walogm.mu.Unlock()
}

// flushMachineSession reads new WAL entries since the last flush, encodes them
// as a JSON array of [elapsed, type, base64] triples, appends them to the
// session's blob_stream via Postgres jsonb concatenation, and increments
// DLP metrics for the flushed window. Safe to call concurrently with writes:
// the WAL mutex serializes writes and reads.
func (p *auditPlugin) flushMachineSession(walogm *walLogRWMutex, pctx plugintypes.Context) error {
	walogm.mu.Lock()
	defer walogm.mu.Unlock()

	startDate, err := walogm.log.Header()
	if err != nil {
		return fmt.Errorf("failed reading wal header: %v", err)
	}

	metrics := newSessionMetric()
	var encoded strings.Builder
	startIndex := walogm.lastFlushedIndex + 1
	if startIndex < 2 {
		startIndex = 2
	}
	protoConnType := pctx.ProtoConnectionType()
	lastIndex, err := walogm.log.ReadFrom(startIndex, func(data []byte) error {
		ev, err := eventlogv1.Decode(data)
		if err != nil {
			return err
		}
		p.encodeEventEntry(&encoded, ev, startDate.StartDate, protoConnType, &metrics)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed reading wal tail: %v", err)
	}
	if lastIndex < startIndex {
		return nil // nothing new since last flush
	}

	chunk := json.RawMessage("[" + encoded.String() + "]")
	if err := models.AppendSessionStream(pctx.OrgID, pctx.SID, chunk); err != nil {
		return fmt.Errorf("failed appending session stream: %v", err)
	}

	walogm.lastFlushedIndex = lastIndex
	walogm.bytesFlushed += int64(len(chunk))

	if err := models.IncrementSessionMaskedMetrics(models.DB, pctx.SID, metrics.DataMasking.InfoTypes); err != nil {
		log.With("sid", pctx.SID).Warnf("failed incrementing session masked metrics, reason=%v", err)
	}
	return nil
}

// closeMachineSession stops the flush ticker, performs a final flush, and
// marks the session as done. The stream blob already exists from OnConnect.
func (p *auditPlugin) closeMachineSession(pctx plugintypes.Context, errMsg error) error {
	walogm, ok := p.walSessionStore.Pop(pctx.SID).(*walLogRWMutex)
	if !ok {
		log.With("sid", pctx.SID).Warnf("no machine session wal found on close")
		// still mark the session done so it doesn't stay open forever
		return p.markMachineSessionDone(pctx, errMsg, 0)
	}
	if walogm.flushCancel != nil {
		walogm.flushCancel()
	}

	// final flush of any unflushed entries
	if errMsg != nil && errMsg != io.EOF {
		walogm.mu.Lock()
		_ = walogm.log.Write(eventlogv1.New(time.Now().UTC(), eventlogv1.ErrorType, []byte(errMsg.Error()), nil))
		walogm.mu.Unlock()
	}
	if err := p.flushMachineSession(walogm, pctx); err != nil {
		log.With("sid", pctx.SID).Warnf("final flush failed: %v", err)
	}

	walogm.mu.Lock()
	_ = walogm.log.Close()
	if err := os.RemoveAll(walogm.folderName); err != nil {
		log.Errorf("failed removing wal file %q, err=%v", walogm.folderName, err)
	}
	bytesFlushed := walogm.bytesFlushed
	walogm.mu.Unlock()

	return p.markMachineSessionDone(pctx, errMsg, bytesFlushed)
}

func (p *auditPlugin) markMachineSessionDone(pctx plugintypes.Context, errMsg error, bytesFlushed int64) error {
	endDate := time.Now().UTC()
	metrics := newSessionMetric()
	metrics.EventSize = bytesFlushed
	metricsMap, err := metrics.toMap()
	if err != nil {
		log.With("sid", pctx.SID).Warnf("failed parsing session metrics: %v", err)
		metricsMap = map[string]any{}
	}
	return models.MarkSessionDone(models.SessionDone{
		ID:         pctx.SID,
		OrgID:      pctx.OrgID,
		Metrics:    metricsMap,
		Status:     string(openapi.SessionStatusDone),
		ExitCode:   parseExitCodeFromErr(errMsg),
		EndSession: &endDate,
	})
}

// drainWALResult holds the output of reading and encoding a WAL's contents.
type drainWALResult struct {
	rawJSONBlobStream string
	metrics           SessionMetric
}

// drainWAL reads all events from a WAL, encodes them as a JSON blob stream, and aggregates DLP metrics.
// Used by the human-session close path (one-shot persist at end of session).
func (p *auditPlugin) drainWAL(walogm *walLogRWMutex, protocolConnectionType pb.ConnectionType, startDate *time.Time) (*drainWALResult, error) {
	var encoded strings.Builder
	metrics := newSessionMetric()
	var err error
	metrics.Truncated, err = walogm.log.ReadFull(func(data []byte) error {
		ev, err := eventlogv1.Decode(data)
		if err != nil {
			return err
		}
		p.encodeEventEntry(&encoded, ev, startDate, protocolConnectionType, &metrics)
		return nil
	})
	if err != nil {
		return nil, err
	}

	rawJSONBlobStream := "[" + encoded.String() + "]"
	metrics.EventSize = int64(len(rawJSONBlobStream))
	return &drainWALResult{rawJSONBlobStream: rawJSONBlobStream, metrics: metrics}, nil
}

// encodeEventEntry appends a decoded event to buf as a `[elapsed, type, base64]`
// triple separated by commas, accumulating DLP metrics as a side effect. Empty
// payloads are skipped. The caller wraps buf contents in `[ ]` to form a
// complete JSON array.
func (p *auditPlugin) encodeEventEntry(buf *strings.Builder, ev *eventlogv1.EventLog, startDate *time.Time, protoConnType pb.ConnectionType, metrics *SessionMetric) {
	accumulateMaskingMetrics(ev, metrics)
	if len(ev.Payload) == 0 {
		return
	}
	stream := p.truncateTCPEventStream(ev.Payload, protoConnType)
	if buf.Len() > 0 {
		buf.WriteByte(',')
	}
	fmt.Fprintf(buf, "[%v,%q,%q]",
		ev.EventTime.Sub(*startDate).Seconds(),
		string(ev.EventType),
		base64.StdEncoding.EncodeToString(stream),
	)
}

// blobFormatFor returns the storage format hint for a connection type, or nil
// when no special encoding is needed (text-based connections).
func blobFormatFor(connType pb.ConnectionType) *string {
	if connType == pb.ConnectionTypePostgres {
		return ptr.String(models.BlobFormatWireProtoType)
	}
	return nil
}

// accumulateMaskingMetrics folds an event's DLP metadata into the running metrics.
func accumulateMaskingMetrics(ev *eventlogv1.EventLog, metrics *SessionMetric) {
	infoEnc := ev.GetMetadata(spectypes.DataMaskingInfoKey)
	if infoEnc == nil {
		return
	}
	dataMaskingInfo, err := spectypes.Decode(infoEnc)
	if err != nil {
		log.Warnf("failed decoding data masking info, reason=%v", err)
		return
	}
	for _, item := range dataMaskingInfo.Items {
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
				continue
			}
			metrics.addInfoType(ts.InfoType, redactPerInfoType)
		}
	}
}

func (p *auditPlugin) writeOnClose(pctx plugintypes.Context, errMsg error) error {
	walFolder := fmt.Sprintf(walFolderTmpl, plugintypes.AuditPath, pctx.OrgID, pctx.SID)
	if wal.FileNotExists(walFolder) {
		// if the wal file does not exist we neet to update the session event stream
		blobStream := fmt.Sprintf(`[ [0, "%s", "%s"] ]`, "e", base64.StdEncoding.EncodeToString([]byte("no log found on disk")))
		emptyMetrics := make(map[string]any, 0)

		blobFormat := blobFormatFor(pctx.ProtoConnectionType())

		endDate := time.Now().UTC()

		trackClient := analytics.New()
		defer trackClient.Close()

		err := models.UpdateSessionEventStream(models.SessionDone{
			ID:         pctx.SID,
			OrgID:      pctx.OrgID,
			Metrics:    emptyMetrics,
			BlobStream: json.RawMessage(blobStream),
			BlobFormat: blobFormat,
			Status:     string(openapi.SessionStatusDone),
			ExitCode:   parseExitCodeFromErr(errMsg),
			EndSession: &endDate,
		})

		trackClient.TrackSessionUsageData(analytics.EventSessionFinished, pctx.OrgID, pctx.UserID, pctx.SID)
		log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "verb", pctx.ClientVerb).
			Infof("finished persisting session to store (empty log), update-session-err=%v, context-err=%v", err, errMsg)

		if err != nil {
			log.With("sid", pctx.SID).Warnf("failed updating session event stream: %v", err)
		}

		log.With("sid", pctx.SID).Debugf("no wal log found on disk for session, path=%v, err=%v", walFolder, err)
		p.walSessionStore.Pop(pctx.SID)
		return err

	}
	// first we gonna try to obtain the wal from memory because could have unflushed logs from memory to disk
	walLogObjMemory := p.walSessionStore.Pop(pctx.SID)
	walogm, ok := walLogObjMemory.(*walLogRWMutex)

	if !ok {
		log.With("sid", pctx.SID).Warnf("failed obtaining write ahead log from memory for session %v", pctx.SID)
		// then if not found on memory we try to open from disk
		// so we can be sure everything is flushed to disk
		walog, _, err := sessionwal.OpenWithHeader(walFolder)
		if err != nil {
			return fmt.Errorf("failed opening wal file to read header, err=%v", err)
		}

		walogm = &walLogRWMutex{
			log:        walog,
			mu:         sync.RWMutex{},
			folderName: walFolder,
		}
	}

	walogm.mu.Lock()
	defer func() { _ = walogm.log.Close(); walogm.mu.Unlock() }()
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
	protocolConnectionType := pctx.ProtoConnectionType()

	result, err := p.drainWAL(walogm, protocolConnectionType, wh.StartDate)
	if err != nil {
		return err
	}

	var blobFormat *string
	switch protocolConnectionType {
	case pb.ConnectionTypePostgres:
		blobFormat = ptr.String(models.BlobFormatWireProtoType)
	}

	endDate := time.Now().UTC()
	sessionMetrics, err := result.metrics.toMap()
	if err != nil {
		log.With("sid", pctx.SID).Warnf("failed parsing session metrics to map, reason=%v", err)
	}

	err = models.UpdateSessionEventStream(models.SessionDone{
		ID:         wh.SessionID,
		OrgID:      wh.OrgID,
		Metrics:    sessionMetrics,
		BlobStream: json.RawMessage(result.rawJSONBlobStream),
		BlobFormat: blobFormat,
		Status:     string(openapi.SessionStatusDone),
		ExitCode:   parseExitCodeFromErr(errMsg),
		EndSession: &endDate,
	})
	log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "verb", pctx.ClientVerb).
		Infof("finished persisting session to store, update-session-err=%v, context-err=%v", err, errMsg)

	err = models.IncrementSessionMaskedMetrics(models.DB, wh.SessionID, result.metrics.DataMasking.InfoTypes)
	if err != nil {
		log.With("sid", pctx.SID).Warnf("failed incrementing session masked metrics, reason=%v", err)
	}

	if err != nil {
		_ = walogm.log.Write(eventlogv1.NewCommitError(endDate, err.Error()))
	} else {
		if err := os.RemoveAll(walogm.folderName); err != nil {
			log.Errorf("failed removing wal file %q, err=%v", walogm.folderName, err)
		}
	}
	return err
}

func (p *auditPlugin) truncateTCPEventStream(eventStream []byte, protoConnType pb.ConnectionType) []byte {
	if len(eventStream) > 5000 && protoConnType == pb.ConnectionTypeTCP {
		return eventStream[0:5000]
	}
	return eventStream
}

func parseExitCodeFromErr(err error) (exitCode *int) {
	switch v := err.(type) {
	case *plugintypes.PacketErr:
		exitCode = v.ExitCode()
	case nil:
		exitCode = func() *int { v := 0; return &v }()
	default:
		exitCode = internalExitCode
	}
	return
}
