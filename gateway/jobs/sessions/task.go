package jobsessions

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/runopsio/hoop/common/log"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/session/eventlog"
	sessionwal "github.com/runopsio/hoop/gateway/session/wal"
	"github.com/runopsio/hoop/gateway/storagev2"
	sessionstorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type (
	eventStreamData struct {
		redactCount      int64
		size             int64
		truncated        bool
		nonIndexedEvents types.SessionNonIndexedEventStreamList
		commitEndDate    *time.Time
		commitError      string
	}
)

func ProcessWalSessions(auditPath string, _ gocron.Job) {
	if err := validateAuditPath(auditPath); err != nil {
		log.Warn(err)
		return
	}
	log := log.With("job", "walsessions")
	walFolders, err := getWalFolders(auditPath)
	if err != nil {
		log.Warn(err)
		return
	}
	log.With("walfolders", len(walFolders)).Infof("job started")
	for count, walFolder := range walFolders {
		count++
		walog, wh, err := sessionwal.OpenWithHeader(walFolder)
		if err != nil {
			log.With("sid", wh.SessionID).Infof("failed opening wal log, err=%v", err)
			continue
		}
		log.With("sid", wh.SessionID).Infof("starting %v/%v", count, len(walFolders))

		defer walog.Close()
		ctx := storagev2.NewContext(wh.UserID, wh.OrgID)
		session, err := sessionstorage.FindOne(ctx, wh.SessionID)
		if err != nil {
			log.With("sid", wh.SessionID).Warnf("failed retrieving session, err=%v", err)
			continue
		}
		ev, err := readFullEventStream(walog, *wh.StartDate)
		if err != nil {
			log.With("sid", wh.SessionID).Warnf("failed reading event streams, err=%v", err)
			continue
		}

		endDate := time.Now().UTC()
		if ev.commitEndDate != nil {
			endDate = *ev.commitEndDate
		}

		// close this session if it's open for more than 48 hours
		t := time.Now().UTC().Add(time.Hour * 48)
		if ev.commitEndDate == nil && t.After(*wh.StartDate) {
			log.With("sid", wh.SessionID).Infof("skip, time condition doesn't match, now=%v (+48h), start-date=%v",
				t.Format(time.RFC3339), wh.StartDate.Format(time.RFC3339))
			continue
		}

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
		labels["processed-by"] = "job-walsessions"
		labels["truncated"] = fmt.Sprintf("%v", ev.truncated)
		labels["commit-error"] = fmt.Sprintf("%v", ev.commitError != "")
		err = pgsession.New().Upsert(ctx, types.Session{
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
			NonIndexedStream: ev.nonIndexedEvents,
			EventSize:        ev.size,
			StartSession:     *wh.StartDate,
			EndSession:       &endDate,
			DlpCount:         ev.redactCount,
		})

		commitErrorMsg := ev.commitError
		if len(commitErrorMsg) > 400 {
			commitErrorMsg = commitErrorMsg[:400]
		}
		log.With(
			"sid", wh.SessionID, "org", wh.OrgID, "email", wh.UserEmail, "connection", wh.ConnectionName,
			"verb", wh.Verb, "size", fmt.Sprintf("%v", ev.size), "started-at", wh.StartDate.Format(time.RFC3339),
			"truncated", ev.truncated, "success", err == nil, "commit-error", commitErrorMsg != "",
		).Infof("processed %v/%v, commit-error-msg:[%s]",
			count, len(walFolders), commitErrorMsg)
		if err != nil {
			log.With("sid", wh.SessionID).Warnf("error=%v", err)
		} else {
			_ = os.RemoveAll(walFolder)
		}
	}
	log.Infof("job finished")
}

func getWalFolders(auditPath string) ([]string, error) {
	dirEntry, err := os.ReadDir(auditPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading audit path, err=%v", err)
	}
	var walFolders []string
	for _, entry := range dirEntry {
		walFolder := filepath.Join(auditPath, entry.Name())
		if entry.IsDir() && strings.HasSuffix(entry.Name(), "-wal") {
			walFolders = append(walFolders, walFolder)
		}
	}
	return walFolders, nil
}

func validateAuditPath(auditPath string) error {
	fs, err := os.Stat(auditPath)
	if err != nil {
		return fmt.Errorf("failed obtaining info about audit path [%v], err=%v", auditPath, err)
	}
	if !fs.IsDir() {
		return fmt.Errorf("audit path [%v] is not a directory", auditPath)
	}
	return nil
}

func readFullEventStream(walog *sessionwal.WalLog, startDate time.Time) (*eventStreamData, error) {
	eventSize := int64(0)
	redactCount := int64(0)
	var commitEndDate *time.Time
	var commitError string

	var eventStreamList []types.SessionEventStream
	truncated, err := walog.ReadFull(func(data []byte) error {
		ev, err := eventlog.DecodeLatest(data)
		if err != nil {
			return err
		}
		if ev.CommitEndDate != nil {
			commitEndDate = ev.CommitEndDate
			commitError = ev.CommitError
			return nil
		}
		eventStreamList = append(eventStreamList, types.SessionEventStream{
			ev.EventTime.Sub(startDate).Seconds(),
			string(ev.EventType),
			base64.StdEncoding.EncodeToString(ev.Data),
		})
		eventSize += int64(len(ev.Data))
		redactCount += int64(ev.RedactCount)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &eventStreamData{
		redactCount:      redactCount,
		size:             eventSize,
		truncated:        truncated,
		nonIndexedEvents: types.SessionNonIndexedEventStreamList{"stream": eventStreamList},
		commitError:      commitError,
		commitEndDate:    commitEndDate,
	}, nil
}
