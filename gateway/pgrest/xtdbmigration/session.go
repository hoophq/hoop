package xtdbmigration

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2"
	sessionstorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

func migrateSessions(xtdbURL, orgID string, dryRun bool, fromDate time.Time, sessionIDs map[string]any) {
	log.Infof("pgrest migration: migrating sessions starting at %v", fromDate.Format(time.RFC3339))
	ctx := storagev2.NewOrganizationContext(orgID, store)
	ctx.SetURL(xtdbURL)
	partialSessionList, err := sessionstorage.ListAllSessionsID(ctx, fromDate)
	if err != nil {
		log.Warnf("pgrest migration: failed listing sessions, err=%v", err)
		return
	}
	log.Infof("pgrest migration: sessions to migrate, total=%v", len(partialSessionList))

	var state migrationState
	for _, s := range partialSessionList {
		if dryRun {
			break
		}
		// skip sessions not in the list
		if sessionIDs != nil {
			if _, ok := sessionIDs[s.ID]; !ok {
				continue
			}
		}
		sid := s.ID
		ctx.UserID = s.UserID
		xsess, err := sessionstorage.FindOne(ctx, sid)
		if err != nil {
			log.Warnf("pgrest migration: failed fetching (xtdb) session, err=%v", err)
			state.failed++
			continue
		}
		// this should never happens
		if xsess == nil {
			log.Warnf("pgrest migration: session not found, id=%v", sid)
			state.failed++
			continue
		}
		hasInputBlobs := xsess.Script["data"] != ""
		hasStreamBlobs := len(xsess.NonIndexedStream["stream"]) > 0
		if pgsess, err := pgsession.New().FetchOne(ctx, sid); pgsess != nil || err != nil {
			if err != nil {
				log.Warnf("pgrest migration: failed fetching session (pgrest) %s, err=%v", sid, err)
				state.failed++
				continue
			}
			// for now only warn about a potential inconsistent problem in the migration
			// if this turns to be a problem, we should implement a process to migrate
			// the blobs in this step.
			inputMigrated := pgsess.Script["data"] != ""
			if !hasInputBlobs {
				inputMigrated = true
			}
			streamMigrated := len(pgsess.NonIndexedStream["stream"]) > 0
			if !hasStreamBlobs {
				streamMigrated = true
			}
			if !inputMigrated || !streamMigrated {
				log.Infof("pgrest migration: missing session blobs, id=%s, status=%v, size=%v, input=%v, stream=%v",
					sid, pgsess.Status, pgsess.EventSize, inputMigrated, streamMigrated)
				state.failed++
				continue
			}

			log.Debugf("pgrest migration: session already migrated %s %s %v %s",
				sid, xsess.StartSession.Format("2006-01-02"), xsess.EventSize, xsess.Status)
			// session state already exists in the target storage
			state.skip++
			continue
		}

		log.Infof("pgrest migration: session %s %s %v %s",
			sid, xsess.StartSession.Format("2006-01-02"), xsess.EventSize, xsess.Status)
		var blobInputID *string
		if hasInputBlobs {
			bid := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("blobinput:%s", xsess.ID))).String()
			blobInputID = &bid
		}
		var blobStreamID *string
		if hasStreamBlobs {
			bid := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("blobstream:%s", xsess.ID))).String()
			blobStreamID = &bid
		}
		sessionStatus := xsess.Status
		var endedAt *string
		if xsess.EndSession != nil {
			et := xsess.EndSession.Format(time.RFC3339Nano)
			endedAt = &et
			// coerce status to done if has the end session date
			if sessionStatus == "" {
				sessionStatus = types.SessionStatusDone
			}
		}

		// coerce old sessions status
		if sessionStatus == "" {
			sessionStatus = types.SessionStatusOpen
			if hasStreamBlobs {
				sessionStatus = types.SessionStatusDone
			}
			log.Warnf("pgrest migration: coerce session status to %s, id=%v", sessionStatus, sid)
		}

		err = pgrest.New("/sessions").Upsert(map[string]any{
			"id":              xsess.ID,
			"org_id":          xsess.OrgID,
			"labels":          xsess.Labels,
			"connection":      xsess.Connection,
			"connection_type": xsess.Type,
			"verb":            xsess.Verb,
			"user_id":         xsess.UserID,
			"user_name":       xsess.UserName,
			"user_email":      xsess.UserEmail,
			"blob_input_id":   blobInputID,
			"blob_stream_id":  blobStreamID,
			"status":          xsess.Status,
			"created_at":      xsess.StartSession.Format(time.RFC3339Nano),
			"ended_at":        endedAt,
			"metadata": map[string]any{
				"redact_count": xsess.DlpCount,
			},
		}).Error()
		if err != nil {
			log.Warnf("pgrest migration: failed migrating session, id=%v, err=%v", sid, err)
			state.failed++
			continue
		}
		if blobInputID != nil {
			err = pgrest.New("/blobs?on_conflict=org_id,id").Upsert(map[string]any{
				"id":          blobInputID,
				"org_id":      xsess.OrgID,
				"type":        "session-input",
				"blob_stream": []any{xsess.Script["data"]},
			}).Error()
			if err != nil {
				log.Warnf("pgrest migration: failed migrating session blob input, id=%v, err=%v", sid, err)
				state.failed++
				continue
			}
		}
		if blobStreamID != nil {
			err = pgrest.New("/blobs").Create(map[string]any{
				"id":          blobStreamID,
				"org_id":      xsess.OrgID,
				"type":        "session-stream",
				"blob_stream": xsess.NonIndexedStream["stream"],
			}).Error()
			if err != nil {
				log.Warnf("pgrest migration: failed migrating session blob stream, id=%v, err=%v", sid, err)
				state.failed++
				continue
			}
		}
		state.success++
	}
	log.Infof("pgrest migration: sessions migrated, total=%v, skip=%v, success=%v, failed=%v",
		len(partialSessionList), state.skip, state.success, state.failed)
}

func migrateReviews(xtdbURL, orgID string) {
	log.Infof("pgrest migration: migrating reviews")
	reviewStore := review.Storage{Storage: storage.New()}
	reviewStore.SetURL(xtdbURL)
	orgCtx := user.NewContext(orgID, "")
	reviewList, err := reviewStore.FindAll(orgCtx)
	if err != nil {
		log.Warnf("pgrest migration: failed listing reviews, err=%v", err)
		return
	}
	var state migrationState
	for _, r := range reviewList {
		rev, err := reviewStore.FindById(orgCtx, r.Id)
		if err != nil {
			log.Warnf("pgrest migration: failed fetching review, err=%v", err)
			state.failed++
			continue
		}
		if rev == nil {
			log.Warnf("pgrest migration: review not found, id=%v", r.Id)
			state.failed++
			continue
		}
		revSid, err := pgreview.New().FetchOneBySid(orgCtx, r.Session)
		if err != nil {
			log.Warnf("pgrest migration: failed fetching review %s by session id, err=%v", r.Id, err)
			state.failed++
			continue
		}
		if revSid != nil {
			continue
		}
		if rev.Type == "" {
			rev.Type = "onetime"
		}
		var parseErr error
		var reviewGroupsData []types.ReviewGroup
		for _, revgroup := range rev.ReviewGroupsData {
			if revgroup.ReviewDate != nil {
				t, err := parseReviewDate(*revgroup.ReviewDate)
				if err != nil {
					parseErr = err
					break
				}
				reviewDate := t.Format(time.RFC3339)
				revgroup.ReviewDate = &reviewDate
			}
			reviewGroupsData = append(reviewGroupsData, revgroup)
		}
		if parseErr != nil {
			log.Warnf("pgrest migration: failed parsing review date, id=%v, err=%v", r.Id, parseErr)
			state.failed++
			continue
		}
		rev.ReviewGroupsData = reviewGroupsData
		if err := pgreview.New().Upsert(rev); err != nil {
			log.Warnf("pgrest migration: failed migrating review, id=%v, err=%v", r.Id, err)
			state.failed++
			continue
		}
		state.success++
	}
	log.Infof("pgrest migration: reviews migrated, total=%v, success=%v, failed=%v",
		len(reviewList), state.success, state.failed)
}

func parseReviewDate(d string) (*time.Time, error) {
	t, err := time.Parse(time.RFC3339, d)
	if err == nil {
		return &t, nil
	}
	t, err = time.Parse(time.RFC3339Nano, d)
	if err == nil {
		return &t, nil
	}
	// remove monotonic clock notation
	d = strings.Split(d, " m=")[0]
	t, err = time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", d)
	if err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("fail to parse date %v", d)
}
