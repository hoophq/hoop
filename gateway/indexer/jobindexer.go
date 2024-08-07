package indexer

import (
	"encoding/base64"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"

	"github.com/blevesearch/bleve/v2"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgplugins "github.com/hoophq/hoop/gateway/pgrest/plugins"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
	"github.com/hoophq/hoop/gateway/storagev2"
	sessionstorage "github.com/hoophq/hoop/gateway/storagev2/session"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var jobMutex = sync.RWMutex{}

// StartJobIndex index sessions in batches based in the period duration
// A session will be indexed if the indexer plugin is enabled;
// the session is closed and it contains an e-mail
//
// The index process start a new fresh index and swap it atomically avoiding any
// downtime in search items in the current index.
//
// See also: http://blevesearch.com/docs/IndexAlias/
func StartJobIndex() error {
	jobMutex.Lock()
	defer jobMutex.Unlock()
	startDate := time.Now().UTC().Add(-defaultIndexPeriod)
	sessionsByOrg, err := listAllSessionsID(startDate)
	if err != nil {
		return err
	}
	for orgID, itemList := range sessionsByOrg {
		orgIDShort := orgID[0:8]
		validateSessionFn, err := fetchIndexerPlugin(orgID)
		if err != nil {
			log.Printf("job=index, org=%s - failed fetching indexer plugin, err=%v", orgIDShort, err)
			return err
		}
		if validateSessionFn == nil {
			continue // it doesn't have the plugin installed, skip it
		}
		newIndex, swapIndexFn, err := newBatchJobIndex(orgID)
		if err != nil {
			log.Printf("job=index, org=%v, failed opening batch job indexes, err=%v", orgID, err)
			continue
		}
		batch, batchCount, indexed, batchErr := newIndex.NewBatch(), 0, 0, error(nil)
		for i, sessionID := range itemList {
			if batchCount >= 30 || i+1 == len(itemList) {
				// log.Printf("job=index, org=%v, batch indexing (%v)", orgIDShort, batchCount)
				batchErr = newIndex.Batch(batch)
				if batchErr != nil {
					indexed = 0
					break
				}
				batch = newIndex.NewBatch()
				batchCount = 0
			}
			storeCtx := storagev2.NewOrganizationContext(orgID)
			sess, err := sessionstorage.FindOne(storeCtx, sessionID)
			if err != nil {
				log.Printf("job=index, org=%v, session=%v - error getting session, reason=%v", orgIDShort, sessionID, err)
				continue
			}
			if sess == nil {
				log.Printf("job=index, org=%v, session=%v - session not found, reason=%v", orgIDShort, sessionID, err)
				continue
			}
			if !validateSessionFn(sess) {
				continue
			}
			if err := batch.Index(sessionID, parseSessionToIndexObject(orgID, sess)); err != nil {
				batchErr = err
				indexed = 0
				break
			}
			batchCount++
			indexed++
		}
		if batchErr == nil {
			batchErr = swapIndexFn()
		}
		log.Printf("job=index, org=%v - period=%vd, success=%v, indexed=%v/%v, error=%v",
			orgID, defaultIndexPeriod.Hours()/24, batchErr == nil, indexed, len(itemList), batchErr)
	}
	return nil
}

func listAllSessionsID(startDate time.Time) (map[string][]string, error) {
	sessionList, err := pgsession.New().FetchAllFromDate(startDate)
	if err != nil {
		return nil, err
	}
	sessionByOrg := map[string][]string{}
	for _, s := range sessionList {
		if _, ok := sessionByOrg[s.OrgID]; ok {
			sessionByOrg[s.OrgID] = append(sessionByOrg[s.OrgID], s.ID)
			continue
		}
		sessionByOrg[s.OrgID] = append(sessionByOrg[s.OrgID], s.ID)
	}
	return sessionByOrg, nil
}

// fetchPlugin retrieve the indexer plugin for the given org
// it returns a closure that validates if the session could be processed
func fetchIndexerPlugin(orgID string) (func(s *types.Session) bool, error) {
	plugin, err := pgplugins.New().FetchOne(pgrest.NewOrgContext(orgID), plugintypes.PluginIndexName)
	if err != nil {
		return nil, err
	}
	if plugin == nil {
		return nil, nil
	}
	pluginMap := map[string]any{}
	for _, conn := range plugin.Connections {
		pluginMap[conn.Name] = nil
	}
	return func(sess *types.Session) bool {
		_, found := pluginMap[sess.Connection]
		return found && sess.EndSession != nil && sess.UserEmail != ""
	}, nil
}

// newBatchJobIndex returns the current index, a new one and
// a function to swap between indexes
func newBatchJobIndex(orgID string) (bleve.Index, func() error, error) {
	currentIndex, err := NewIndexer(orgID)
	if err != nil {
		return nil, nil, err
	}
	newIndex, updateStateFileFn, err := newBleveIndex(orgID)
	if err != nil {
		return nil, nil, err
	}
	return newIndex, func() error {
		if err := updateStateFileFn(); err != nil {
			return err
		}
		return currentIndex.swapIndex(newIndex)
	}, nil
}

func parseSessionToIndexObject(orgID string, s *types.Session) *Session {
	var stdinData []byte
	var stdoutData []byte

	truncateStdin, truncateStdout := false, false
	for _, eventList := range s.EventStream {
		event, ok := eventList.(types.SessionEventStream)
		if !ok {
			log.Warnf("job=index, fail to type cast to types.SessionEventStream, found=%T", eventList)
			continue
		}
		stdin, stdout := parseEventStream(event)
		stdinData = append(stdinData, stdin...)
		stdoutData = append(stdoutData, stdout...)
		if len(stdinData) > MaxIndexSize {
			stdinData = stdinData[0:MaxIndexSize]
			truncateStdin = true
		}
		if len(stdoutData) > MaxIndexSize {
			stdoutData = stdoutData[0:MaxIndexSize]
			truncateStdin = true
		}
		if truncateStdin && truncateStdout {
			break
		}
	}
	durationInSecs := int64(s.EndSession.Sub(s.StartSession).Seconds())
	return &Session{
		OrgID:             orgID,
		ID:                s.ID,
		User:              s.UserEmail,
		Connection:        s.Connection,
		ConnectionType:    s.Type,
		Verb:              s.Verb,
		EventSize:         s.EventSize,
		Input:             string(stdinData),
		Output:            string(stdoutData),
		IsInputTruncated:  truncateStdin,
		IsOutputTruncated: truncateStdout,
		// TODO: add iserror to audit sessions!
		// IsError:   false,
		StartDate: s.StartSession.Format(time.RFC3339),
		EndDate:   s.EndSession.Format(time.RFC3339),
		Duration:  durationInSecs,
	}
}

func parseEventStream(event types.SessionEventStream) (stdin, stdout []byte) {
	eventType, _ := event[1].(string)
	eventData, _ := base64.StdEncoding.DecodeString(event[2].(string))
	switch eventType {
	case "i":
		stdin = []byte(eventData)
	case "o", "e":
		stdout = []byte(eventData)
	}
	return
}
