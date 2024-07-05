package review

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	"github.com/hoophq/hoop/gateway/review"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

func (r *reviewPlugin) onReceiveOSS(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if pctx.ClientVerb != pb.ClientVerbConnect {
		return nil, fmt.Errorf(`review is enabled for this connection, it allows only interacting via "hoop connect"`)
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
	// reviewType := review.ReviewTypeOneTime
	accessDuration := time.Duration(time.Minute * 30)
	if durationStr, ok := pkt.Spec[pb.SpecJitTimeout]; ok {
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
		Type:            review.ReviewTypeJit,
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
	log.With("session", pctx.SID, "id", newRev.Id, "user", pctx.UserID, "org", pctx.OrgID,
		"type", review.ReviewTypeJit, "duration", fmt.Sprintf("%vm", accessDuration.Minutes())).
		Infof("creating review")
	if err := r.reviewSvc.Persist(pctx, newRev); err != nil {
		return nil, plugintypes.InternalErr("failed saving review", err)
	}
	return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
		Type:    pbclient.SessionOpenWaitingApproval,
		Payload: []byte(fmt.Sprintf("%s/plugins/reviews/%s", r.apiURL, newRev.Id)),
		Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(pctx.SID)},
	}}, nil

}
