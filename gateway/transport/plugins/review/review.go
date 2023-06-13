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
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
)

type reviewPlugin struct {
	apiURL              string
	reviewSvc           *review.Service
	userSvc             *user.Service
	notificationService notification.Service
}

func New(reviewSvc *review.Service, userSvc *user.Service, notificationSvc notification.Service, apiURL string) *reviewPlugin {
	return &reviewPlugin{
		apiURL:              apiURL,
		reviewSvc:           reviewSvc,
		userSvc:             userSvc,
		notificationService: notificationSvc,
	}
}

func (r *reviewPlugin) Name() string                          { return plugintypes.PluginReviewName }
func (r *reviewPlugin) OnStartup(_ plugintypes.Context) error { return nil }
func (p *reviewPlugin) OnUpdate(_, _ *types.Plugin) error     { return nil }
func (r *reviewPlugin) OnConnect(_ plugintypes.Context) error { return nil }

func (r *reviewPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if pkt.Type != pbagent.SessionOpen {
		return nil, nil
	}

	userContext := &user.Context{
		Org:  &user.Org{Id: pctx.OrgID},
		User: &user.User{Id: pctx.UserID},
	}

	otrev, err := r.reviewSvc.FindBySessionID(pctx.SID)
	if err != nil {
		err = fmt.Errorf("hala big bonga")
		log.With("session", pctx.SID).Error("failed fetching session, err=%v", err)
		return nil, plugintypes.InternalErr("failed fetching review", err)
	}

	if otrev != nil && otrev.Type == review.ReviewTypeOneTime {
		log.With("id", otrev.Id, "session", pctx.SID, "user", otrev.CreatedBy, "org", pctx.OrgID,
			"status", otrev.Status).Infof("one time review")
		if !(otrev.Status == types.ReviewStatusApproved || otrev.Status == types.ReviewStatusProcessing) {
			reviewURL := fmt.Sprintf("%s/plugins/reviews/%s", r.apiURL, otrev.Id)
			return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
				Type:    pbclient.SessionOpenWaitingApproval,
				Payload: []byte(reviewURL),
			}}, nil
		}

		if otrev.Status == types.ReviewStatusApproved {
			otrev.Status = types.ReviewStatusProcessing
			if err := r.reviewSvc.Persist(userContext, otrev); err != nil {
				return nil, plugintypes.InternalErr("failed saving approved review", err)
			}
		}
		return nil, nil
	}

	jitr, err := r.reviewSvc.FindApprovedJitReviews(userContext, pctx.ConnectionID)
	if err != nil {
		return nil, plugintypes.InternalErr("failed listing time based reviews", err)
	}
	if jitr != nil {
		log.With("session", pctx.SID, "id", jitr.Id, "user", jitr.CreatedBy, "org", pctx.OrgID,
			"revoke-at", jitr.RevokeAt.Format(time.RFC3339),
			"duration", fmt.Sprintf("%vm", jitr.AccessDuration.Minutes())).Infof("jit access granted")
		newCtx, _ := context.WithTimeout(pctx.Context, jitr.AccessDuration)
		return &plugintypes.ConnectResponse{Context: newCtx, ClientPacket: nil}, nil
	}

	var accessDuration time.Duration
	reviewType := review.ReviewTypeOneTime
	durationStr, isJitReview := pkt.Spec[pb.SpecJitTimeout]
	if isJitReview {
		reviewType = review.ReviewTypeJit
		accessDuration, err = time.ParseDuration(string(durationStr))
		if err != nil {
			return nil, plugintypes.InvalidArgument("invalid access time duration, got=%#v", string(durationStr))
		}
		if accessDuration.Hours() > 48 {
			return nil, plugintypes.InvalidArgument("jit access input must not be greater than 48 hours")
		}
	}

	if len(pctx.PluginConnectionConfig) == 0 {
		err = fmt.Errorf("missing approval groups for connection")
		return nil, plugintypes.InternalErr(err.Error(), err)
	}

	reviewGroups := make([]types.ReviewGroup, 0)
	groups := make([]string, 0)
	for _, s := range pctx.PluginConnectionConfig {
		groups = append(groups, s)
		reviewGroups = append(reviewGroups, types.ReviewGroup{
			Group:  s,
			Status: types.ReviewStatusPending,
		})
	}

	newRev := &types.Review{
		Id:           uuid.NewString(),
		Type:         reviewType,
		OrgId:        pctx.OrgID,
		CreatedAt:    time.Now().UTC(),
		Session:      pctx.SID,
		Input:        "",
		ConnectionId: pctx.ConnectionID,
		Connection: types.ReviewConnection{
			Id:   pctx.ConnectionID,
			Name: pctx.ConnectionName,
		},
		CreatedBy: pctx.UserID,
		ReviewOwner: types.ReviewOwner{
			Id:    pctx.UserID,
			Name:  pctx.UserName,
			Email: pctx.UserEmail,
		},
		AccessDuration: accessDuration,
		Status:         types.ReviewStatusPending,
		ReviewGroups:   reviewGroups,
	}

	if !isJitReview {
		// only onetime reviews has input
		newRev.Input = string(pkt.Payload)
	}

	log.With("session", pctx.SID, "id", newRev.Id, "user", pctx.UserID, "org", pctx.OrgID,
		"type", reviewType, "duration", fmt.Sprintf("%vm", accessDuration.Minutes())).
		Infof("creating review")
	if err := r.reviewSvc.Persist(userContext, newRev); err != nil {
		return nil, plugintypes.InternalErr("failed saving review", err)
	}

	reviewers, err := r.userSvc.FindByGroups(userContext, groups)
	if err != nil {
		return nil, plugintypes.InternalErr("failed obtaining approvers", err)
	}

	reviewersEmail := listEmails(reviewers)
	r.notificationService.Send(notification.Notification{
		Title:      "[hoop.dev] Pending review",
		Message:    r.buildReviewUrl(newRev.Id),
		Recipients: reviewersEmail,
	})
	return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
		Type:    pbclient.SessionOpenWaitingApproval,
		Payload: []byte(fmt.Sprintf("%s/plugins/reviews/%s", r.apiURL, newRev.Id)),
	}}, nil
}

func (r *reviewPlugin) OnDisconnect(_ plugintypes.Context, errMsg error) error { return nil }
func (r *reviewPlugin) OnShutdown()                                            {}

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
