package sessionstorage

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func FindOne(storageCtx *storagev2.Context, reviewID string) (*types.Review, error) {
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?r [:xt/id
			:review/type
			:review/status
			:review/access-duration
			:review/created-at
			:review/revoke-at
			:review/input
			:review/session
			:review/connection
			:review/review-groups-data
			:review/created-by
				{:review/connection [:xt/id :connection/name]}
				{:review/review-groups [*
					{:review-group/reviewed-by [:xt/id :user/name :user/email]}]}
				{:review/created-by [:xt/id :user/name :user/email]}])]
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

	return &reviews[0][0], nil
}
