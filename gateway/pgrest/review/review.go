package pgreview

import (
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/pgrest"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type review struct{}

func New() *review { return &review{} }

func (r *review) Upsert(rev *types.Review) error {
	var blobInputID *string
	if rev.Input != "" {
		blobInputID = toStringPtr(uuid.NewString())
	}
	var createdAt string
	if !rev.CreatedAt.IsZero() {
		createdAt = rev.CreatedAt.Format(time.RFC3339)
	}

	err := pgrest.New("/reviews?on_conflict=org_id,session_id").Upsert(map[string]any{
		"id":                  rev.Id,
		"org_id":              rev.OrgId,
		"connection_id":       toStringPtr(rev.Connection.Id),
		"connection_name":     rev.Connection.Name,
		"session_id":          toStringPtr(rev.Session),
		"type":                rev.Type,
		"input_env_vars":      rev.InputEnvVars,
		"input_client_args":   rev.InputClientArgs,
		"access_duration_sec": int(rev.AccessDuration.Seconds()),
		"blob_input_id":       blobInputID,
		"status":              rev.Status,
		"owner_id":            rev.ReviewOwner.Id,
		"owner_email":         rev.ReviewOwner.Email,
		"owner_name":          rev.ReviewOwner.Name,
		"owner_slack_id":      rev.ReviewOwner.SlackID,
		"revoked_at":          rev.RevokeAt,
		// required only for migrating resources from xtdb to postgrest
		"created_at": toStringPtr(createdAt),
	}).Error()
	if err != nil {
		return fmt.Errorf("failed creating or updating review, err=%v", err)
	}
	if blobInputID != nil {
		err := pgrest.New("/blobs").Create(map[string]any{
			"id":          blobInputID,
			"org_id":      rev.OrgId,
			"type":        "review-input",
			"blob_stream": []any{rev.Input},
		}).Error()
		if err != nil {
			return fmt.Errorf("failed creating or updating blob input, err=%v", err)
		}
	}
	var reqBody []map[string]any
	for _, revgroup := range rev.ReviewGroupsData {
		payload := map[string]any{
			"id":          revgroup.Id,
			"org_id":      rev.OrgId,
			"review_id":   rev.Id,
			"group_name":  revgroup.Group,
			"status":      revgroup.Status,
			"reviewed_at": revgroup.ReviewDate,
		}
		var reviewedBy types.ReviewOwner
		if revgroup.ReviewedBy != nil {
			reviewedBy = *revgroup.ReviewedBy
		}
		payload["owner_id"] = reviewedBy.Id
		payload["owner_email"] = reviewedBy.Email
		payload["owner_name"] = reviewedBy.Name
		payload["owner_slack_id"] = reviewedBy.SlackID
		reqBody = append(reqBody, payload)
	}
	if len(reqBody) > 0 {
		if err := pgrest.New("/review_groups").Upsert(reqBody).Error(); err != nil {
			return fmt.Errorf("failed creating or updating review groups, err=%v", err)
		}
	}
	return nil
}

func (r *review) FetchAll(ctx pgrest.OrgContext) ([]types.Review, error) {
	var items []Review
	err := pgrest.New("/reviews?org_id=eq.%s&select=*,review_groups(*)", ctx.GetOrgID()).
		List().
		DecodeInto(&items)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	var result []types.Review
	for _, r := range items {
		result = append(result, *parseReview(r))
	}
	return result, nil
}

func (r *review) FetchOneByID(ctx pgrest.OrgContext, reviewID string) (*types.Review, error) {
	var rev Review
	err := pgrest.New("/reviews?org_id=eq.%s&id=eq.%s&select=*,review_groups(*),blob_input(*)",
		ctx.GetOrgID(), url.QueryEscape(reviewID)).
		FetchOne().
		DecodeInto(&rev)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return parseReview(rev), nil
}

func (r *review) FetchOneBySid(ctx pgrest.OrgContext, sid string) (*types.Review, error) {
	var rev Review
	err := pgrest.New("/reviews?org_id=eq.%s&session_id=eq.%s&select=*,review_groups(*),blob_input(*)", ctx.GetOrgID(), url.QueryEscape(sid)).
		FetchOne().
		DecodeInto(&rev)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return parseReview(rev), nil
}

func (r *review) FetchJit(ctx pgrest.OrgContext, ownerUserID, connectionID string) (*types.Review, error) {
	var rev Review
	err := pgrest.New("/reviews?org_id=eq.%s&type=eq.jit&status=eq.APPROVED&owner_id=eq.%s&connection_id=eq.%s&select=*,review_groups(*)&order=created_at.desc&limit=1",
		ctx.GetOrgID(),
		url.QueryEscape(ownerUserID),
		url.QueryEscape(connectionID),
	).FetchOne().DecodeInto(&rev)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return parseReview(rev), nil
}

func ToJson(rev types.Review) *types.ReviewJSON {
	return &types.ReviewJSON{
		Id:        rev.Id,
		OrgId:     rev.OrgId,
		CreatedAt: rev.CreatedAt,
		Type:      rev.Type,
		Session:   rev.Session,
		Input:     rev.Input,
		// Redacted for now
		// InputEnvVars:     rev.InputEnvVars,
		InputClientArgs:  rev.InputClientArgs,
		AccessDuration:   rev.AccessDuration,
		Status:           rev.Status,
		RevokeAt:         rev.RevokeAt,
		ReviewOwner:      rev.ReviewOwner,
		ReviewGroupsData: rev.ReviewGroupsData,
		Connection: types.ReviewConnection{
			Id:   rev.Connection.Id,
			Name: rev.Connection.Name,
		},
	}
}

func parseReview(r Review) *types.Review {
	result := &types.Review{
		Id:              r.ID,
		OrgId:           r.OrgID,
		CreatedAt:       r.GetCreatedAt(),
		Type:            r.Type,
		Session:         toString(r.SessionID),
		Input:           r.GetBlobInput(),
		InputEnvVars:    r.InputEnvVars,
		InputClientArgs: r.InputClientArgs,
		AccessDuration:  r.GetAccessDuration(),
		Status:          types.ReviewStatus(r.Status),
		RevokeAt:        r.GetRevokedAt(),
		CreatedBy:       r.OwnerUserID,
		ReviewOwner: types.ReviewOwner{
			Id:      r.OwnerUserID,
			Email:   r.OwnerEmail,
			Name:    toString(r.OwnerName),
			SlackID: toString(r.OwnerSlackID),
		},
		Connection: types.ReviewConnection{
			Id:   toString(r.ConnectionID),
			Name: r.ConnectionName,
		},
		// the connection id is expanded is used to perform a join on xtdb
		// when the entity exists this field is a map, otherwise is a string containing the xtid
		ConnectionId:    r.ConnectionID,
		ReviewGroupsIds: []string{},
	}
	for _, rg := range r.ReviewGroups {
		revGroup := types.ReviewGroup{
			Id:         rg.ID,
			Group:      rg.GroupName,
			Status:     types.ReviewStatus(rg.Status),
			ReviewedBy: nil,
			ReviewDate: rg.ReviewedAt,
		}
		if rg.OwnerUserID != nil {
			revGroup.ReviewedBy = &types.ReviewOwner{
				Id:      toString(rg.OwnerUserID),
				Email:   toString(rg.OwnerEmail),
				Name:    toString(rg.OwnerName),
				SlackID: toString(rg.OwnerSlackID),
			}
		}
		result.ReviewGroupsData = append(result.ReviewGroupsData, revGroup)
	}
	return result
}

// update the status of a review
func (r *review) PatchStatus(ctx pgrest.OrgContext, reviewID string, status types.ReviewStatus) error {
	return pgrest.New("/reviews?org_id=eq.%s&id=eq.%s", ctx.GetOrgID(), url.QueryEscape(reviewID)).
		Patch(map[string]any{"status": status}).
		Error()
}
