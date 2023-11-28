package reviewstorage

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

// intercepted on api layer
func FindOne(storageCtx *storagev2.Context, reviewID string) (*types.Review, error) {
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?r [*
						{:review/connection [:xt/id :connection/name]}
						{:review/created-by [:xt/id :user/name :user/email :user/slack-id]}])]
		:in [org-id review-id]
		:where [[?r :review/org org-id]
				[?r :xt/id review-id]
				[?r :review/connection connid]
				[?c :xt/id connid]]}
        :in-args [%q %q]}`, storageCtx.OrgID, reviewID)

	b, err := storageCtx.Query(payload)
	if err != nil {
		return nil, err
	}

	var reviews [][]types.Review
	if err := edn.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}

	if len(reviews) == 0 {
		return nil, nil
	}

	reviewData := reviews[0][0]

	groups, err := findGroupsByReviewId(storageCtx, reviewData.Id)
	if err != nil {
		return nil, err
	}

	reviewData.ReviewGroupsData = groups

	return &reviewData, nil
}

func findGroupsByReviewId(storageCtx *storagev2.Context, reviewID string) ([]types.ReviewGroup, error) {
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?r [{:review/review-groups 
			[* {:review-group/reviewed-by [:xt/id :user/name :user/email]}]}])]
		:in [org-id review-id]
		:where [[?r :review/org org-id]
				[?r :xt/id review-id]]}
        :in-args [%q %q]}`, storageCtx.OrgID, reviewID)

	b, err := storageCtx.Query(payload)
	if err != nil {
		return nil, err
	}

	type ReviewGroups struct {
		ReviewGroups []types.ReviewGroup `edn:"review/review-groups"`
	}

	var reviewsGroups [][]ReviewGroups
	if err := edn.Unmarshal(b, &reviewsGroups); err != nil {
		return nil, err
	}

	if len(reviewsGroups) == 0 {
		return nil, nil
	}

	return reviewsGroups[0][0].ReviewGroups, nil
}
