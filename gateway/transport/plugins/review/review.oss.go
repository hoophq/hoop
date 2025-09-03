package review

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

func (p *reviewPlugin) onReceiveOSS(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if pctx.ClientVerb != pb.ClientVerbConnect {
		return nil, fmt.Errorf(`Accessing a connection with review from the web requires an Enterprise plan. Contact us for instant access to a 15-day trial license - no strings attached. If you want to continue using the OSS version, you can access your connection from the CLI or the Hoop desktop app. Check our docs for more information: https://hoop.dev/docs/clients/cli`)
	}
	jitr, err := models.GetApprovedReviewJit(pctx.OrgID, pctx.UserEmail, pctx.ConnectionID)
	if err != nil && err != models.ErrNotFound {
		return nil, plugintypes.InternalErr("failed listing time based reviews", err)
	}
	if jitr != nil {
		err = validateJit(jitr, time.Now().UTC())
		switch err {
		case errJitExpired: // it's expired, must not proceed without creating a jit record
		case nil: // jit is valid, grant access
			log.With("sid", pctx.SID, "id", jitr.ID, "user", jitr.OwnerEmail, "org", pctx.OrgID,
				"revoke-at", jitr.RevokedAt.Format(time.RFC3339),
				"duration", fmt.Sprintf("%vs", jitr.AccessDurationSec)).Infof("jit access granted")
			newCtx, _ := context.WithTimeout(pctx.Context, time.Duration(jitr.AccessDurationSec)*time.Second)
			return &plugintypes.ConnectResponse{Context: newCtx, ClientPacket: nil}, nil
		default:
			return nil, err
		}
	}
	log.With("sid", pctx.SID, "orgid", pctx.GetOrgID(), "user-id", pctx.UserID, "connection-id", pctx.ConnectionID).
		Infof("jit review not found")

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

	var reviewGroups []models.ReviewGroups
	for _, approvalGroupName := range pctx.PluginConnectionConfig {
		reviewGroups = append(reviewGroups, models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     pctx.OrgID,
			GroupName: approvalGroupName,
			Status:    models.ReviewStatusPending,
		})
	}

	newRev := &models.Review{
		ID:                uuid.NewString(),
		OrgID:             pctx.OrgID,
		Type:              models.ReviewTypeJit,
		SessionID:         pctx.SID,
		ConnectionName:    pctx.ConnectionName,
		ConnectionID:      sql.NullString{String: pctx.ConnectionID, Valid: true},
		AccessDurationSec: int64(accessDuration.Seconds()),
		InputEnvVars:      nil,
		InputClientArgs:   nil,
		OwnerID:           pctx.UserID,
		OwnerEmail:        pctx.UserEmail,
		OwnerName:         ptr.String(pctx.UserName),
		OwnerSlackID:      ptr.String(pctx.UserSlackID),
		Status:            models.ReviewStatusPending,
		ReviewGroups:      reviewGroups,
		CreatedAt:         time.Now().UTC(),
		RevokedAt:         nil,
	}

	p.setSpecReview(pkt)
	log.With("sid", pctx.SID, "id", newRev.ID, "user", pctx.UserID, "org", pctx.OrgID,
		"type", "jit", "duration", fmt.Sprintf("%vm", accessDuration.Minutes())).
		Infof("creating review")

	// input is always empty for jit types
	var sessionInput string
	if err := models.CreateReview(newRev, sessionInput); err != nil {
		return nil, plugintypes.InternalErr("failed saving review", err)
	}
	return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
		Type:    pbclient.SessionOpenWaitingApproval,
		Payload: fmt.Appendf(nil, "%s/reviews/%s", p.apiURL, newRev.ID),
		Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(pctx.SID)},
	}}, nil

}
