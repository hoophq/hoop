package xtdbmigration

import (
	"fmt"
	"strings"
	"time"

	"github.com/runopsio/hoop/common/log"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2"
	sessionstorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

func migrateSessions(xtdbURL, orgID string, fromDate time.Time) {
	log.Infof("pgrest migration: migrating sessions")
	sessionIDList, err := sessionstorage.ListAllSessionsID(fromDate)
	if err != nil {
		log.Warnf("pgrest migration: failed listing sessions, err=%v", err)
		return
	}
	ctx := storagev2.NewOrganizationContext(orgID, store)
	ctx.SetURL(xtdbURL)
	var state migrationState
	for _, s := range sessionIDList {
		sid := s.ID
		sess, err := sessionstorage.FindOne(ctx, sid)
		if err != nil {
			log.Warnf("pgrest migration: failed fetching session, err=%v", err)
			state.failed++
			continue
		}
		if sess == nil {
			log.Warnf("pgrest migration: session not found, id=%v", sid)
			state.failed++
			continue
		}
		log.Infof("pgrest migration: migrating session, id=%s, event-size=%v, conn=%s, status=%s, useremail=%s",
			sid, sess.EventSize, sess.Connection, sess.Status, sess.UserEmail)
		if err := pgsession.New().Upsert(ctx, *sess); err != nil {
			log.Warnf("pgrest migration: failed migrating session, id=%v, err=%v", sid, err)
			state.failed++
			continue
		}
		state.success++
	}
	log.Infof("pgrest migration: sessions migrated, total=%v, success=%v, failed=%v",
		len(sessionIDList), state.success, state.failed)
}

func migrateReviews(xtdbURL, orgID string) {
	log.Infof("pgrest migration: migrating reviews")
	reviewStore := review.Storage{Storage: storage.New()}
	reviewStore.SetURL(xtdbURL)
	reviewList, err := reviewStore.FindAll(user.NewContext(orgID, ""))
	if err != nil {
		log.Warnf("pgrest migration: failed listing reviews, err=%v", err)
		return
	}
	var state migrationState
	for _, r := range reviewList {
		rev, err := reviewStore.FindById(user.NewContext(orgID, ""), r.Id)
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
	// remove monotonic clock notation
	d = strings.Split(d, " m=")[0]
	t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", d)
	if err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("fail to parse date %v", d)
}
