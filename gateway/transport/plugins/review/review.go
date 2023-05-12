package review

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/review"
	rv "github.com/runopsio/hoop/gateway/review"
	transporterr "github.com/runopsio/hoop/gateway/transport/errors"
	"github.com/runopsio/hoop/gateway/user"
)

const Name string = "review"

type reviewPlugin struct {
	name                string
	apiURL              string
	reviewSvc           *review.Service
	userSvc             *user.Service
	notificationService notification.Service
}

func New(reviewSvc *review.Service, userSvc *user.Service, notificationSvc notification.Service, apiURL string) *reviewPlugin {
	return &reviewPlugin{
		name:                Name,
		apiURL:              apiURL,
		reviewSvc:           reviewSvc,
		userSvc:             userSvc,
		notificationService: notificationSvc,
	}
}

func (r *reviewPlugin) Name() string                         { return r.name }
func (r *reviewPlugin) OnStartup(config plugin.Config) error { return nil }
func (r *reviewPlugin) OnConnect(config plugin.Config) error {
	log.With("session", config.SessionId).Infof("review on-connect")
	return nil
}

func (r *reviewPlugin) OnReceive(pconf plugin.Config, config []string, pkt *pb.Packet) error {
	if pkt.Type != pbagent.SessionOpen {
		return nil
	}

	userContext := &user.Context{
		Org:  &user.Org{Id: pconf.Org},
		User: &user.User{Id: pconf.UserID},
	}

	otrev, err := r.reviewSvc.FindBySessionID(pconf.SessionId)
	if err != nil {
		log.With("session", pconf.SessionId).Error("failed fetching session, err=%v", err)
		return transporterr.Internal("internal error, failed fetching review", err)
	}

	if otrev != nil && otrev.Type == review.ReviewTypeOneTime {
		log.With("id", otrev.Id, "session", pconf.SessionId, "user", otrev.CreatedBy, "org", pconf.Org,
			"status", otrev.Status).Infof("one time review")
		if !(otrev.Status == rv.StatusApproved || otrev.Status == rv.StatusProcessing) {
			reviewURL := fmt.Sprintf("%s/plugins/reviews/%s", r.apiURL, otrev.Id)
			return transporterr.Noop(&pb.Packet{
				Type:    pbclient.SessionOpenWaitingApproval,
				Payload: []byte(reviewURL),
			})
		}

		if otrev.Status == rv.StatusApproved {
			otrev.Status = rv.StatusProcessing
			if err := r.reviewSvc.Persist(userContext, otrev); err != nil {
				return transporterr.Internal("failed saving approved review", err)
			}
		}
		return nil
	}

	jitr, err := r.reviewSvc.FindApprovedJitReviews(userContext, pconf.ConnectionId)
	if err != nil {
		return transporterr.Internal("failed listing time based reviews", err)
	}
	if jitr != nil {
		log.With("session", pconf.SessionId, "id", jitr.Id, "user", jitr.CreatedBy, "org", pconf.Org,
			"revoke-at", jitr.RevokeAt.Format(time.RFC3339),
			"duration", fmt.Sprintf("%vm", jitr.AccessDuration.Minutes())).Infof("jit access granted")
		newCtx, _ := context.WithTimeout(pconf.ConnectionContext, jitr.AccessDuration)
		return transporterr.NoopContext(newCtx)
	}

	var accessDuration time.Duration
	reviewType := review.ReviewTypeOneTime
	durationStr, isJitReview := pkt.Spec[pb.SpecJitTimeout]
	if isJitReview {
		reviewType = review.ReviewTypeJit
		accessDuration, err = time.ParseDuration(string(durationStr))
		if err != nil {
			return transporterr.InvalidArgument("invalid access time duration, got=%#v", string(durationStr))
		}
		if accessDuration.Hours() > 48 {
			return transporterr.InvalidArgument("jit access input must not be greater than 48 hours")
		}
	}

	if len(config) == 0 {
		err = fmt.Errorf("missing approval groups for connection")
		return transporterr.Internal(err.Error(), err)
	}

	reviewGroups := make([]rv.Group, 0)
	groups := make([]string, 0)
	for _, s := range config {
		groups = append(groups, s)
		reviewGroups = append(reviewGroups, rv.Group{
			Group:  s,
			Status: rv.StatusPending,
		})
	}

	newRev := &rv.Review{
		Id:      uuid.NewString(),
		Type:    reviewType,
		Session: pconf.SessionId,
		Connection: rv.Connection{
			Id:   pconf.ConnectionId,
			Name: pconf.ConnectionName,
		},
		AccessDuration: accessDuration,
		Status:         rv.StatusPending,
		ReviewGroups:   reviewGroups,
	}
	if !isJitReview {
		// only onetime reviews has input
		newRev.Input = string(pkt.Payload)
	}

	log.With("session", pconf.SessionId, "id", newRev.Id, "user", pconf.UserID, "org", pconf.Org,
		"type", reviewType, "duration", fmt.Sprintf("%vm", accessDuration.Minutes())).
		Infof("creating review")
	if err := r.reviewSvc.Persist(userContext, newRev); err != nil {
		return transporterr.Internal("failed saving review", err)
	}

	reviewers, err := r.userSvc.FindByGroups(userContext, groups)
	if err != nil {
		return transporterr.Internal("failed obtaining approvers", err)
	}

	reviewersEmail := listEmails(reviewers)
	r.notificationService.Send(notification.Notification{
		Title:      "[hoop.dev] Pending review",
		Message:    r.buildReviewUrl(newRev.Id),
		Recipients: reviewersEmail,
	})
	return transporterr.Noop(&pb.Packet{
		Type:    pbclient.SessionOpenWaitingApproval,
		Payload: []byte(fmt.Sprintf("%s/plugins/reviews/%s", r.apiURL, newRev.Id)),
	})
}

func (r *reviewPlugin) OnDisconnect(config plugin.Config, errMsg error) error { return nil }
func (r *reviewPlugin) OnShutdown()                                           {}

func (r *reviewPlugin) buildReviewUrl(reviewID string) string {
	url := fmt.Sprintf("%s/plugins/reviews/%s", r.apiURL, reviewID)
	return fmt.Sprintf("A user is waiting for your review at hoop.dev.\n\n Visit %s for more information.\n\n Hoop Team.", url)
}

func listEmails(reviewers []user.User) []string {
	emails := make([]string, 0)
	for _, r := range reviewers {
		emails = append(emails, r.Email)
	}
	return emails
}
