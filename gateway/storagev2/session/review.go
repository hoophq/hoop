package sessionstorage

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func PutReview(ctx *storagev2.Context, review *types.Review) (int64, error) {
	if review.Id == "" {
		review.Id = uuid.NewString()
	}
	if pgrest.Rollout {
		// the usage of this function is only to update the status of the review
		return 0, pgreview.New().PatchStatus(ctx, review.Id, review.Status)
	}

	for i, r := range review.ReviewGroupsData {
		if r.Id == "" {
			review.ReviewGroupsData[i].Id = uuid.NewString()
		}
	}
	reviewGroupIds := make([]string, 0)

	var payloads []types.TxObject
	for _, r := range review.ReviewGroupsData {
		reviewGroupIds = append(reviewGroupIds, r.Id)
		xg := &types.ReviewGroup{
			Id:         r.Id,
			Group:      r.Group,
			Status:     r.Status,
			ReviewDate: r.ReviewDate,
		}
		if r.ReviewedBy != nil {
			xg.ReviewedBy = r.ReviewedBy
		}
		payloads = append(payloads, xg)
	}

	xtdbReview := &types.Review{
		Id:               review.Id,
		CreatedAt:        review.CreatedAt,
		OrgId:            review.OrgId,
		Type:             review.Type,
		Session:          review.Session,
		Connection:       review.Connection,
		ConnectionId:     review.ConnectionId,
		CreatedBy:        review.CreatedBy,
		ReviewOwner:      review.ReviewOwner,
		Input:            review.Input,
		AccessDuration:   review.AccessDuration,
		RevokeAt:         review.RevokeAt,
		Status:           review.Status,
		ReviewGroupsIds:  reviewGroupIds,
		ReviewGroupsData: review.ReviewGroupsData,
	}

	tx, err := ctx.Put(append(payloads, xtdbReview)...)
	if err != nil {
		return 0, err
	}

	return tx.TxID, nil
}
