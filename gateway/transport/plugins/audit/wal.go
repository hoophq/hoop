package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/session/eventbroker"
	eventlogv1 "github.com/hoophq/hoop/gateway/session/eventlog/v1"
	sessionwal "github.com/hoophq/hoop/gateway/session/wal"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// <audit-path>/<orgid>-<sessionid>-wal
const walFolderTmpl string = `%s/%s-%s-wal`

// flushInterval is how often a session's WAL tail is flushed to the session's
// blob_stream during the session.
const flushInterval = 50 * time.Second

var internalExitCode = func() *int { v := 254; return &v }()

// errFlushCapReached signals that a flush has reached the persisted-stream size
// cap (sessionwal.DefaultMaxRead) and should stop reading further WAL entries.
var errFlushCapReached = errors.New("flush size cap reached")

type walLogRWMutex struct {
	log        *sessionwal.WalLog
	mu         sync.RWMutex
	folderName string

	// incremental-flush / streaming state
	lastFlushedIndex uint64
	bytesFlushed     int64         // cumulative encoded bytes appended to blob_stream (→ event_size)
	bytesRead        int64         // cumulative raw WAL bytes read; enforces the DefaultMaxRead cap
	truncated        bool          // set once bytesRead exceeds the cap; halts further flushing
	closed           bool          // set when the WAL is closed; guards a late ticker flush
	metrics          SessionMetric // cumulative DLP metrics across flushes, persisted to the session JSONB at close
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
	p.walSessionStore.Set(pctx.SID, &walLogRWMutex{log: walog, folderName: walFolder, metrics: newSessionMetric()})
	return nil
}

// startFlushTicker creates the empty stream blob and spawns a goroutine that
// flushes new WAL entries to blob_stream every flushInterval. The flush
// goroutine stops when the returned cancel func is invoked (typically at
// OnDisconnect, which then performs a final flush).
func (p *auditPlugin) startFlushTicker(pctx plugintypes.Context) error {
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
		ticker := time.NewTicker(flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.flushSession(walogm, pctx); err != nil {
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

	// feed the live SSE broker as events arrive (no-op when nobody is subscribed)
	eventbroker.Default.Publish(pctx.SID, eventbroker.Event{
		Time:    now,
		Type:    string(eventType),
		Payload: event,
	})
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
	walogm.closed = true
	_ = walogm.log.Close()
	if err := os.RemoveAll(walogm.folderName); err != nil {
		log.Errorf("failed removing wal file %q, err=%v", walogm.folderName, err)
	}
	walogm.mu.Unlock()
}

// flushSession reads new WAL entries since the last flush, encodes them as a
// JSON array of [elapsed, type, base64] triples, appends them to the session's
// blob_stream via Postgres jsonb concatenation, accumulates the cumulative DLP
// metrics, and increments the per-info-type masked metrics for the flushed
// window. It stops appending once the persisted stream reaches
// sessionwal.DefaultMaxRead bytes, flagging the session as truncated. Safe to
// call concurrently with writes: the WAL mutex serializes writes and reads.
func (p *auditPlugin) flushSession(walogm *walLogRWMutex, pctx plugintypes.Context) error {
	walogm.mu.Lock()
	defer walogm.mu.Unlock()

	if walogm.closed {
		return nil // a late ticker tick lost the race with close; WAL is gone
	}
	if walogm.truncated {
		return nil // reached the persisted-stream cap; stop appending
	}

	header, err := walogm.log.Header()
	if err != nil {
		return fmt.Errorf("failed reading wal header: %v", err)
	}

	windowMetrics := newSessionMetric()
	var encoded strings.Builder
	startIndex := max(walogm.lastFlushedIndex+1, 2)
	protoConnType := pctx.ProtoConnectionType()
	capReached := false
	lastIndex, err := walogm.log.ReadFrom(startIndex, func(data []byte) error {
		ev, derr := eventlogv1.Decode(data)
		if derr != nil {
			return derr
		}
		p.encodeEventEntry(&encoded, ev, header.StartDate, protoConnType, &windowMetrics)
		walogm.bytesRead += int64(len(data))
		if walogm.bytesRead > sessionwal.DefaultMaxRead {
			capReached = true
			return errFlushCapReached
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFlushCapReached) {
		return fmt.Errorf("failed reading wal tail: %v", err)
	}
	if lastIndex < startIndex && !capReached {
		return nil // nothing new since last flush
	}

	if encoded.Len() > 0 {
		chunk := json.RawMessage("[" + encoded.String() + "]")
		if err := models.AppendSessionStream(pctx.OrgID, pctx.SID, chunk); err != nil {
			return fmt.Errorf("failed appending session stream: %v", err)
		}
		walogm.bytesFlushed += int64(len(chunk))
	}

	if capReached {
		// the entry that crossed the cap (lastIndex+1) was already encoded above
		walogm.lastFlushedIndex = lastIndex + 1
		walogm.truncated = true
	} else {
		walogm.lastFlushedIndex = lastIndex
	}
	walogm.metrics.merge(windowMetrics)

	if err := models.IncrementSessionMaskedMetrics(models.DB, pctx.SID, windowMetrics.DataMasking.InfoTypes); err != nil {
		log.With("sid", pctx.SID).Warnf("failed incrementing session masked metrics, reason=%v", err)
	}
	return nil
}

// closeSessionWAL stops the flush ticker, performs a final flush of any
// unflushed WAL entries, persists the session as done, and removes the WAL.
// Three cases are handled:
//
//  1. no WAL on disk — the WAL was never created or was dropped (e.g. reviewed
//     sessions); persist a placeholder stream and mark the session done.
//  2. WAL held in memory (normal path) — write any trailing error, final flush,
//     then mark done with the cumulative metrics gathered during the session.
//  3. WAL on disk but not in memory — in-flight flush state was lost (e.g. a
//     gateway restart); mark done without re-reading to avoid duplicating
//     entries already appended to blob_stream.
func (p *auditPlugin) closeSessionWAL(pctx plugintypes.Context, errMsg error) error {
	walFolder := fmt.Sprintf(walFolderTmpl, plugintypes.AuditPath, pctx.OrgID, pctx.SID)

	// case 1
	if sessionwal.FileNotExists(walFolder) {
		p.walSessionStore.Pop(pctx.SID)
		return p.markSessionDonePlaceholder(pctx, errMsg)
	}

	walogm, ok := p.walSessionStore.Pop(pctx.SID).(*walLogRWMutex)
	if !ok {
		// case 3
		log.With("sid", pctx.SID).Warnf("wal not in memory on close; marking done without re-flush")
		return p.markSessionDone(pctx, errMsg, newSessionMetric())
	}

	// case 2
	if walogm.flushCancel != nil {
		walogm.flushCancel()
	}
	if errMsg != nil && errMsg != io.EOF {
		walogm.mu.Lock()
		_ = walogm.log.Write(eventlogv1.New(time.Now().UTC(), eventlogv1.ErrorType, []byte(errMsg.Error()), nil))
		walogm.mu.Unlock()
	}
	if err := p.flushSession(walogm, pctx); err != nil {
		log.With("sid", pctx.SID).Warnf("final flush failed: %v", err)
	}

	walogm.mu.Lock()
	walogm.closed = true
	_ = walogm.log.Close()
	if err := os.RemoveAll(walogm.folderName); err != nil {
		log.Errorf("failed removing wal file %q, err=%v", walogm.folderName, err)
	}
	metrics := walogm.metrics
	metrics.EventSize = walogm.bytesFlushed
	metrics.Truncated = walogm.truncated
	walogm.mu.Unlock()

	return p.markSessionDone(pctx, errMsg, metrics)
}

// markSessionDone marks the session terminal columns and merges the cumulative
// session metrics into the session row. The stream blob has already been
// written incrementally by the flushes.
func (p *auditPlugin) markSessionDone(pctx plugintypes.Context, errMsg error, metrics SessionMetric) error {
	endDate := time.Now().UTC()
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

// markSessionDonePlaceholder persists a placeholder stream for sessions whose
// WAL was never created or was dropped, then marks the session done.
func (p *auditPlugin) markSessionDonePlaceholder(pctx plugintypes.Context, errMsg error) error {
	blobStream := fmt.Sprintf(`[ [0, "%s", "%s"] ]`, "e", base64.StdEncoding.EncodeToString([]byte("no log found on disk")))
	endDate := time.Now().UTC()
	err := models.UpdateSessionEventStream(models.SessionDone{
		ID:         pctx.SID,
		OrgID:      pctx.OrgID,
		Metrics:    map[string]any{},
		BlobStream: json.RawMessage(blobStream),
		BlobFormat: blobFormatFor(pctx.ProtoConnectionType()),
		Status:     string(openapi.SessionStatusDone),
		ExitCode:   parseExitCodeFromErr(errMsg),
		EndSession: &endDate,
	})
	log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "verb", pctx.ClientVerb).
		Infof("finished persisting session to store (empty log), update-session-err=%v, context-err=%v", err, errMsg)
	if err != nil {
		log.With("sid", pctx.SID).Warnf("failed updating session event stream: %v", err)
	}
	return err
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
