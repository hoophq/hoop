package review

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

type reviewPlugin struct {
	apiURL string
}

func New(apiURL string) *reviewPlugin {
	return &reviewPlugin{
		apiURL: apiURL,
	}
}

func (p *reviewPlugin) Name() string                                   { return plugintypes.PluginReviewName }
func (p *reviewPlugin) OnStartup(_ plugintypes.Context) error          { return nil }
func (p *reviewPlugin) OnUpdate(_, _ plugintypes.PluginResource) error { return nil }
func (p *reviewPlugin) OnConnect(_ plugintypes.Context) error          { return nil }
func (p *reviewPlugin) getReviewersFromPlugin(pctx plugintypes.Context) []models.ReviewGroups {
	var reviewGroups []models.ReviewGroups
	for _, approvalGroupName := range pctx.PluginConnectionConfig {
		reviewGroups = append(reviewGroups, models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     pctx.OrgID,
			GroupName: approvalGroupName,
			Status:    models.ReviewStatusPending,
		})
	}

	return reviewGroups
}
func (p *reviewPlugin) getReviewersFromAccessRequestRule(pctx plugintypes.Context, rule *models.AccessRequestRule) ([]models.ReviewGroups, error) {
	var reviewersGroups []string

	if rule.AllGroupsMustApprove {
		groups, err := models.GetUserGroupsByOrgID(pctx.OrgID)
		if err != nil {
			return nil, plugintypes.InternalErr("failed fetching user groups for org", err)
		}

		for _, approvalGroup := range groups {
			reviewersGroups = append(reviewersGroups, approvalGroup.Name)
		}
	} else {
		reviewersGroups = rule.ReviewersGroups
	}

	var reviewGroups []models.ReviewGroups
	for _, approvalGroupName := range reviewersGroups {
		reviewGroups = append(reviewGroups, models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     pctx.OrgID,
			GroupName: approvalGroupName,
			Status:    models.ReviewStatusPending,
		})
	}

	return reviewGroups, nil
}

func (p *reviewPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if pkt.Type != pbagent.SessionOpen {
		return nil, nil
	}
	if pctx.OrgLicenseType == license.OSSType {
		return p.onReceiveOSS(pctx, pkt)
	}

	otrev, err := models.GetReviewByIdOrSid(pctx.OrgID, pctx.SID)
	if err != nil && err != models.ErrNotFound {
		log.With("sid", pctx.SID).Error("failed fetching session, err=%v", err)
		return nil, plugintypes.InternalErr("failed fetching review", err)
	}

	if otrev != nil && otrev.Type == models.ReviewTypeOneTime {
		log.With("id", otrev.ID, "sid", pctx.SID, "user", otrev.OwnerEmail, "org", pctx.OrgID,
			"status", otrev.Status).Info("one time review")
		if !(otrev.Status == models.ReviewStatusApproved || otrev.Status == models.ReviewStatusProcessing) {
			reviewURL := fmt.Sprintf("%s/reviews/%s", p.apiURL, otrev.ID)
			p.setSpecReview(pkt)
			return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
				Type:    pbclient.SessionOpenWaitingApproval,
				Payload: []byte(reviewURL),
				Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(pctx.SID)},
			}}, nil
		}

		if otrev.Status == models.ReviewStatusApproved {
			if err := models.UpdateReviewStatus(otrev.OrgID, otrev.ID, models.ReviewStatusProcessing); err != nil {
				return nil, plugintypes.InternalErr("failed updating approved review", err)
			}
		}
		return nil, nil
	}

	jitr, err := models.GetApprovedReviewJit(pctx.OrgID, pctx.UserID, pctx.ConnectionID)
	if err != nil && err != models.ErrNotFound {
		return nil, plugintypes.InternalErr("failed listing time based reviews", err)
	}
	if jitr != nil {
		err = validateJit(jitr, time.Now().UTC())
		switch err {
		case errJitExpired: // it's expired, must not proceed without creating a jit record
		case nil: // jit is valid
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

	var accessDuration time.Duration
	durationStr, isJitReview := pkt.Spec[pb.SpecJitTimeout]
	accessType := "command"
	if isJitReview {
		accessType = "jit"
	}

	orgID, err := uuid.Parse(pctx.OrgID)
	if err != nil {
		return nil, plugintypes.InvalidArgument("invalid organization id format")
	}

	accessRule, err := models.GetAccessRequestRuleByResourceNameAndAccessType(models.DB, orgID, pctx.ConnectionName, accessType)
	if err != nil && err != models.ErrNotFound {
		return nil, plugintypes.InternalErr("failed fetching access request rule", err)
	}

	if isJitReview {
		accessDuration, err = time.ParseDuration(string(durationStr))
		if err != nil {
			return nil, plugintypes.InvalidArgument("invalid access time duration, got=%v", string(durationStr))
		}

		connection, err := models.GetConnectionByNameOrID(pctx, pctx.ConnectionID)
		if err != nil {
			return nil, plugintypes.InternalErr("failed fetching connection", err)
		}

		var accessMaxDuration *int
		if accessRule != nil {
			accessMaxDuration = accessRule.AccessMaxDuration
		} else {
			accessMaxDuration = connection.AccessMaxDuration
		}

		if accessMaxDuration != nil {
			maxDuration := time.Duration(*accessMaxDuration) * time.Second

			if accessDuration > maxDuration {
				return nil, plugintypes.InvalidArgument("jit access input exceeds connection max duration of %vs",
					maxDuration.Seconds())
			}
		} else if accessDuration.Hours() > 48 {
			return nil, plugintypes.InvalidArgument("jit access input must not be greater than 48 hours")
		}
	}

	if len(pctx.PluginConnectionConfig) == 0 {
		err = fmt.Errorf("missing approval groups for connection")
		return nil, plugintypes.InternalErr(err.Error(), err)
	}

	var reviewGroups []models.ReviewGroups
	if accessRule != nil {
		reviewGroups, err = p.getReviewersFromAccessRequestRule(pctx, accessRule)
		if err != nil {
			return nil, err
		}
	} else {
		reviewGroups = p.getReviewersFromPlugin(pctx)
	}

	reviewType := models.ReviewTypeOneTime
	if isJitReview {
		reviewType = models.ReviewTypeJit
	}

	// these values are only used for ad-hoc executions
	var sessionInput string
	var inputEnvVars map[string]string
	var inputClientArgs []string
	if !isJitReview {
		sessionInput = string(pkt.Payload)
		if encInputEnvVars, ok := pkt.Spec[pb.SpecClientExecEnvVar]; ok {
			if err := pb.GobDecodeInto(encInputEnvVars, &inputEnvVars); err != nil {
				return nil, plugintypes.InternalErr("failed decoding input env vars", err)
			}
		}
		if encInputClientArgs, ok := pkt.Spec[pb.SpecClientExecArgsKey]; ok {
			if err := pb.GobDecodeInto(encInputClientArgs, &inputClientArgs); err != nil {
				return nil, plugintypes.InternalErr("failed decoding input client args", err)
			}
		}
	}

	newRev := &models.Review{
		ID:                uuid.NewString(),
		OrgID:             pctx.OrgID,
		Type:              reviewType,
		SessionID:         pctx.SID,
		ConnectionName:    pctx.ConnectionName,
		ConnectionID:      sql.NullString{String: pctx.ConnectionID, Valid: true},
		AccessDurationSec: int64(accessDuration.Seconds()),
		InputEnvVars:      inputEnvVars,
		InputClientArgs:   inputClientArgs,
		OwnerID:           pctx.UserID,
		OwnerEmail:        pctx.UserEmail,
		OwnerName:         ptr.String(pctx.UserName),
		OwnerSlackID:      ptr.String(pctx.UserSlackID),
		Status:            models.ReviewStatusPending,
		ReviewGroups:      reviewGroups,
		CreatedAt:         time.Now().UTC(),
		RevokedAt:         nil,
	}

	// update session input when executing ad-hoc executions via cli
	if strings.HasPrefix(pctx.ClientOrigin, pb.ConnectionOriginClient) {
		if err := models.UpdateSessionInput(pctx.OrgID, pctx.SID, sessionInput); err != nil {
			return nil, plugintypes.InternalErr("failed updating session input", err)
		}
	}

	p.setSpecReview(pkt)
	log.With("sid", pctx.SID, "id", newRev.ID, "user", pctx.UserID, "org", pctx.OrgID,
		"type", reviewType, "duration", fmt.Sprintf("%vm", accessDuration.Minutes())).
		Infof("creating review")

	if err := models.CreateReview(newRev, sessionInput); err != nil {
		return nil, plugintypes.InternalErr("failed saving review", err)
	}

	return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
		Type:    pbclient.SessionOpenWaitingApproval,
		Payload: fmt.Appendf(nil, "%s/reviews/%s", p.apiURL, newRev.ID),
		Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(pctx.SID)},
	}}, nil
}

func (p *reviewPlugin) OnDisconnect(_ plugintypes.Context, errMsg error) error { return nil }
func (p *reviewPlugin) OnShutdown()                                            {}

// indicate to other plugins that this packet has the review enabled
// it will allow applying special logic for these cases
func (p *reviewPlugin) setSpecReview(pkt *pb.Packet) { pkt.Spec[pb.SpecHasReviewKey] = []byte("true") }

var errJitExpired = errors.New("jit expired")

func validateJit(jit *models.ReviewJit, t time.Time) error {
	if jit.RevokedAt == nil || jit.RevokedAt.IsZero() {
		return plugintypes.InternalErr("found inconsistent jit record",
			fmt.Errorf("revoked_at attribute is empty for %s", jit.ID))
	}
	revokedAt := jit.RevokedAt.Format(time.RFC3339Nano)
	isJitExpired := jit.RevokedAt.Before(t)
	log.With("id", jit.ID, "created-at", jit.CreatedAt.Format(time.RFC3339Nano), "expired", isJitExpired).
		Infof("validating jit, now=%v, revoked-at=%v",
			t.Format(time.RFC3339Nano), revokedAt)
	if isJitExpired {
		return errJitExpired
	}
	return nil
}
