package review

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/license"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
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
		log.With("session", pctx.SID).Error("failed fetching session, err=%v", err)
		return nil, plugintypes.InternalErr("failed fetching review", err)
	}

	if otrev != nil && otrev.Type == review.ReviewTypeOneTime {
		log.With("id", otrev.Id, "session", pctx.SID, "user", otrev.ReviewOwner.Email, "org", pctx.OrgID,
			"status", otrev.Status).Info("one time review")
		if !(otrev.Status == types.ReviewStatusApproved || otrev.Status == types.ReviewStatusProcessing) {
			reviewURL := fmt.Sprintf("%s/plugins/reviews/%s", p.apiURL, otrev.Id)
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

	jitr, err := pgreview.New().FetchJit(pctx, pctx.ConnectionID)
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

	log.With("session", pctx.SID, "id", newRev.Id, "user", pctx.UserID, "org", pctx.OrgID,
		"type", reviewType, "duration", fmt.Sprintf("%vm", accessDuration.Minutes())).
		Infof("creating review")
	if err := p.reviewSvc.Persist(pctx, newRev); err != nil {
		return nil, plugintypes.InternalErr("failed saving review", err)
	}
	return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
		Type:    pbclient.SessionOpenWaitingApproval,
		Payload: []byte(fmt.Sprintf("%s/plugins/reviews/%s", p.apiURL, newRev.Id)),
		Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(pctx.SID)},
	}}, nil
}

func (p *reviewPlugin) OnDisconnect(_ plugintypes.Context, errMsg error) error { return nil }
func (p *reviewPlugin) OnShutdown()                                            {}
