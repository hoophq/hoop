package accessrequestinterceptor

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
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/utils"
	"gorm.io/gorm"
)

func getValidatedJitReview(pctx plugintypes.Context) (*plugintypes.ConnectResponse, error) {
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

	return nil, nil
}

func getValidatedOneTimeReview(pctx plugintypes.Context) (bool, *plugintypes.ConnectResponse, error) {
	otrev, err := models.GetReviewByIdOrSid(pctx.OrgID, pctx.SID)
	if err != nil && err != models.ErrNotFound {
		log.With("sid", pctx.SID).Error("failed fetching session, err=%v", err)
		return false, nil, plugintypes.InternalErr("failed fetching review", err)
	}

	if otrev != nil && otrev.Type == models.ReviewTypeOneTime {
		log.With("id", otrev.ID, "sid", pctx.SID, "user", otrev.OwnerEmail, "org", pctx.OrgID,
			"status", otrev.Status).Info("one time review")
		if !(otrev.Status == models.ReviewStatusApproved || otrev.Status == models.ReviewStatusProcessing) {
			apiURL := appconfig.Get().FullApiURL()
			reviewURL := fmt.Sprintf("%s/reviews/%s", apiURL, otrev.ID)
			return false, &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
				Type:    pbclient.SessionOpenWaitingApproval,
				Payload: []byte(reviewURL),
				Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(pctx.SID)},
			}}, nil
		}

		if otrev.Status == models.ReviewStatusApproved {
			if err := models.UpdateReviewStatus(otrev.OrgID, otrev.ID, models.ReviewStatusProcessing); err != nil {
				return false, nil, plugintypes.InternalErr("failed updating approved review", err)
			}
		}

		// if the review is already approved or processing, just continue without returning a response
		return true, nil, nil
	}

	return false, nil, nil
}

func createReview(pctx plugintypes.Context, isJitReview bool, reviewersGroups []string, accessDuration time.Duration, sessionInput string, inputEnvVars map[string]string, inputClientArgs []string) (*models.Review, error) {
	var reviewGroups []models.ReviewGroups
	for _, approvalGroupName := range reviewersGroups {
		reviewGroups = append(reviewGroups, models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     pctx.OrgID,
			GroupName: approvalGroupName,
			Status:    models.ReviewStatusPending,
		})
	}

	reviewType := models.ReviewTypeOneTime
	if isJitReview {
		reviewType = models.ReviewTypeJit
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

	log.With("sid", pctx.SID, "id", newRev.ID, "user", pctx.UserID, "org", pctx.OrgID,
		"type", reviewType, "duration", fmt.Sprintf("%vm", accessDuration.Minutes())).
		Infof("creating review")

	if err := models.CreateReview(newRev, sessionInput); err != nil {
		return nil, plugintypes.InternalErr("failed saving review", err)
	}

	// update session input when executing ad-hoc executions via cli
	if strings.HasPrefix(pctx.ClientOrigin, pb.ConnectionOriginClient) {
		if err := models.UpdateSessionInput(pctx.OrgID, pctx.SID, sessionInput); err != nil {
			return nil, plugintypes.InternalErr("failed updating session input", err)
		}
	}

	return newRev, nil
}

func OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if pctx.ClientVerb == pb.ClientVerbPlainExec {
		log.With("sid", pctx.SID, "orgid", pctx.OrgID, "user-id", pctx.UserID, "connection-id", pctx.ConnectionID).
			Infof("skipping access review for plain exec")

		return nil, nil
	}

	if pkt.Type != pbagent.SessionOpen {
		return nil, nil
	}

	if pctx.OrgLicenseType == license.OSSType {
		if pctx.ClientVerb != pb.ClientVerbConnect {
			return nil, fmt.Errorf(`Accessing a connection with review from the web requires an Enterprise plan. Contact us for instant access to a 15-day trial license - no strings attached. If you want to continue using the OSS version, you can access your connection from the CLI or the Hoop desktop app. Check our docs for more information: https://hoop.dev/docs/clients/cli`)
		}
	}

	// 1. check if there's an existing one-time review for this session, if yes validate and return it
	isApproved, resp, err := getValidatedOneTimeReview(pctx)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		setSpecReview(pkt)
		return resp, nil
	}
	if isApproved {
		return nil, nil
	}

	// 2. check if there's an existing jit review for this connection, if yes validate and return it
	resp, err = getValidatedJitReview(pctx)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		setSpecReview(pkt)
		return resp, nil
	}

	// 3. if no existing review, create a new review
	orgID, err := uuid.Parse(pctx.OrgID)
	if err != nil {
		return nil, plugintypes.InvalidArgument("invalid organization id format")
	}

	durationStr, isJitReview := pkt.Spec[pb.SpecJitTimeout]
	accessType := "command"
	if isJitReview {
		accessType = "jit"
	}

	accessRule, err := models.GetAccessRequestRuleByResourceNameAndAccessType(models.DB, orgID, pctx.ConnectionName, accessType)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, plugintypes.InternalErr("failed fetching access request rule", err)
	}

	if accessRule == nil {
		log.With("sid", pctx.SID, "orgid", pctx.OrgID, "user-id", pctx.UserID, "connection-id", pctx.ConnectionID,
			"access-type", accessType).Infof("no access rule found for this resource and access type")
		return nil, nil
	}

	if len(accessRule.ApprovalRequiredGroups) > 0 {
		needsReview := utils.SlicesHasIntersection(accessRule.ApprovalRequiredGroups, pctx.UserGroups)
		if !needsReview {
			log.With("sid", pctx.SID, "orgid", pctx.GetOrgID(), "user-id", pctx.UserID, "connection-id", pctx.ConnectionID,
				"access-rule-id", accessRule.ID).Infof("user is not part of access rule approval groups, skipping review")
			return nil, nil
		}
	}

	// Access duration for JIT reviews
	var accessDuration time.Duration
	if isJitReview {
		accessDuration, err = time.ParseDuration(string(durationStr))
		if err != nil {
			return nil, plugintypes.InvalidArgument("invalid access time duration, got=%v", string(durationStr))
		}

		var accessMaxDuration = accessRule.AccessMaxDuration
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

	newRev, err := createReview(pctx, isJitReview, accessRule.ReviewersGroups, accessDuration, sessionInput, inputEnvVars, inputClientArgs)
	if err != nil {
		return nil, err
	}

	setSpecReview(pkt)

	apiURL := appconfig.Get().FullApiURL()
	return &plugintypes.ConnectResponse{Context: nil, ClientPacket: &pb.Packet{
		Type:    pbclient.SessionOpenWaitingApproval,
		Payload: fmt.Appendf(nil, "%s/reviews/%s", apiURL, newRev.ID),
		Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(pctx.SID)},
	}}, nil
}

// indicate to other plugins that this packet has the review enabled
// it will allow applying special logic for these cases
func setSpecReview(pkt *pb.Packet) { pkt.Spec[pb.SpecHasReviewKey] = []byte("true") }

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
