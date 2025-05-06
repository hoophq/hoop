package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	"github.com/hoophq/hoop/gateway/review"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

type reviewPlugin struct {
	apiURL    string
	reviewSvc *review.Service
}

func New(reviewSvc *review.Service, apiURL string) *reviewPlugin {
	return &reviewPlugin{
		apiURL:    apiURL,
		reviewSvc: reviewSvc,
	}
}

func (p *reviewPlugin) Name() string                          { return plugintypes.PluginReviewName }
func (p *reviewPlugin) OnStartup(_ plugintypes.Context) error { return nil }
func (p *reviewPlugin) OnUpdate(_, _ *types.Plugin) error     { return nil }
func (p *reviewPlugin) OnConnect(_ plugintypes.Context) error { return nil }

func (p *reviewPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if pkt.Type != pbagent.SessionOpen {
		return nil, nil
	}
	if pctx.OrgLicenseType == license.OSSType {
		return p.onReceiveOSS(pctx, pkt)
	}

	otrev, err := pgreview.New().FetchOneBySid(pctx, pctx.SID)
	if err != nil {
		log.With("sid", pctx.SID).Error("failed fetching session, err=%v", err)
		return nil, plugintypes.InternalErr("failed fetching review", err)
	}

	if otrev != nil && otrev.Type == review.ReviewTypeOneTime {
		log.With("id", otrev.Id, "sid", pctx.SID, "user", otrev.ReviewOwner.Email, "org", pctx.OrgID,
			"status", otrev.Status).Info("one time review")
		if !(otrev.Status == types.ReviewStatusApproved || otrev.Status == types.ReviewStatusProcessing) {
			reviewURL := fmt.Sprintf("%s/reviews/%s", p.apiURL, otrev.Id)
			p.setSpecReview(pkt)
			return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
				Type:    pbclient.SessionOpenWaitingApproval,
				Payload: []byte(reviewURL),
				Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(pctx.SID)},
			}}, nil
		}

		if otrev.Status == types.ReviewStatusApproved {
			otrev.Status = types.ReviewStatusProcessing
			if err := p.reviewSvc.Persist(pctx, otrev); err != nil {
				return nil, plugintypes.InternalErr("failed saving approved review", err)
			}
		}
		return nil, nil
	}

	jitr, err := pgreview.New().FetchJit(pctx, pctx.UserID, pctx.ConnectionID)
	if err != nil {
		return nil, plugintypes.InternalErr("failed listing time based reviews", err)
	}
	if jitr != nil {
		err = validateJit(jitr, time.Now().UTC())
		switch err {
		case errJitExpired: // it's expired, must not proceed without creating a jit record
		case nil: // jit is valid
			log.With("sid", pctx.SID, "id", jitr.Id, "user", jitr.CreatedBy, "org", pctx.OrgID,
				"revoke-at", jitr.RevokeAt.Format(time.RFC3339),
				"duration", fmt.Sprintf("%vm", jitr.AccessDuration.Minutes())).Infof("jit access granted")
			newCtx, _ := context.WithTimeout(pctx.Context, jitr.AccessDuration)
			return &plugintypes.ConnectResponse{Context: newCtx, ClientPacket: nil}, nil
		default:
			return nil, err
		}
	}
	log.With("sid", pctx.SID, "orgid", pctx.GetOrgID(), "user-id", pctx.UserID, "connection-id", pctx.ConnectionID).
		Infof("jit review not found")

	var accessDuration time.Duration
	reviewType := review.ReviewTypeOneTime
	durationStr, isJitReview := pkt.Spec[pb.SpecJitTimeout]
	if isJitReview {
		reviewType = review.ReviewTypeJit
		accessDuration, err = time.ParseDuration(string(durationStr))
		if err != nil {
			return nil, plugintypes.InvalidArgument("invalid access time duration, got=%v", string(durationStr))
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

	var inputClientArgs []string
	if encInputClientArgs, ok := pkt.Spec[pb.SpecClientExecArgsKey]; ok {
		if err := pb.GobDecodeInto(encInputClientArgs, &inputClientArgs); err != nil {
			return nil, plugintypes.InternalErr("failed decoding input client args", err)
		}
	}
	newRev := &types.Review{
		Id:              uuid.NewString(),
		Type:            reviewType,
		OrgId:           pctx.OrgID,
		CreatedAt:       time.Now().UTC(),
		Session:         pctx.SID,
		Input:           "",
		InputEnvVars:    nil,
		InputClientArgs: inputClientArgs,
		ConnectionId:    pctx.ConnectionID,
		Connection: types.ReviewConnection{
			Id:   pctx.ConnectionID,
			Name: pctx.ConnectionName,
		},
		CreatedBy: pctx.UserID,
		ReviewOwner: types.ReviewOwner{
			Id:      pctx.UserID,
			Name:    pctx.UserName,
			Email:   pctx.UserEmail,
			SlackID: pctx.UserSlackID,
		},
		AccessDuration:   accessDuration,
		Status:           types.ReviewStatusPending,
		ReviewGroupsIds:  groups,
		ReviewGroupsData: reviewGroups,
	}

	if !isJitReview {
		// only onetime reviews has inputs
		var inputEnvVars map[string]string
		if encInputEnvVars, ok := pkt.Spec[pb.SpecClientExecEnvVar]; ok {
			if err := pb.GobDecodeInto(encInputEnvVars, &inputEnvVars); err != nil {
				return nil, plugintypes.InternalErr("failed decoding input env vars", err)
			}
		}
		newRev.Input = string(pkt.Payload)
		newRev.InputEnvVars = inputEnvVars
	}

	p.setSpecReview(pkt)
	log.With("sid", pctx.SID, "id", newRev.Id, "user", pctx.UserID, "org", pctx.OrgID,
		"type", reviewType, "duration", fmt.Sprintf("%vm", accessDuration.Minutes())).
		Infof("creating review")
	if err := p.reviewSvc.Persist(pctx, newRev); err != nil {
		return nil, plugintypes.InternalErr("failed saving review", err)
	}

	return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
		Type:    pbclient.SessionOpenWaitingApproval,
		Payload: fmt.Appendf(nil, "%s/reviews/%s", p.apiURL, newRev.Id),
		Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(pctx.SID)},
	}}, nil
}

func (p *reviewPlugin) OnDisconnect(_ plugintypes.Context, errMsg error) error { return nil }
func (p *reviewPlugin) OnShutdown()                                            {}

// indicate to other plugins that this packet has the review enabled
// it will allow applying special logic for these cases
func (p *reviewPlugin) setSpecReview(pkt *pb.Packet) { pkt.Spec[pb.SpecHasReviewKey] = []byte("true") }

var errJitExpired = errors.New("jit expired")

func validateJit(jit *types.Review, t time.Time) error {
	if jit.RevokeAt == nil || jit.RevokeAt.IsZero() {
		return plugintypes.InternalErr("found inconsistent jit record",
			fmt.Errorf("revoked_at attribute is empty for %s", jit.Id))
	}
	revokedAt := jit.RevokeAt.Format(time.RFC3339Nano)
	isJitExpired := jit.RevokeAt.Before(t)
	log.With("id", jit.Id, "created-at", jit.CreatedAt.Format(time.RFC3339Nano), "expired", isJitExpired).
		Infof("validating jit, now=%v, revoked-at=%v",
			t.Format(time.RFC3339Nano), revokedAt)
	if isJitExpired {
		return errJitExpired
	}
	return nil
}
