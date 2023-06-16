package api

import (
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func SanitizeReview(review types.Review) *types.ReviewJSON {
	reviewOwnerMap, _ := review.CreatedBy.(map[any]any)
	if reviewOwnerMap == nil {
		reviewOwnerMap = map[any]any{
			edn.Keyword("xt/id"):      "",
			edn.Keyword("user/name"):  "",
			edn.Keyword("user/email"): "",
		}
	}

	reviewConnectionMap, _ := review.ConnectionId.(map[any]any)
	if reviewConnectionMap == nil {
		reviewConnectionMap = map[any]any{
			edn.Keyword("xt/id"):           "",
			edn.Keyword("connection/name"): "",
		}
	}

	reviewOwnerToStringFn := func(key string) string {
		v, _ := reviewOwnerMap[edn.Keyword(key)].(string)
		return v
	}

	connectionToStringFn := func(key string) string {
		v, _ := reviewConnectionMap[edn.Keyword(key)].(string)
		return v
	}

	reviewJSON := types.ReviewJSON{
		Id:             review.Id,
		OrgId:          review.OrgId,
		CreatedAt:      review.CreatedAt,
		Type:           review.Type,
		Session:        review.Session,
		Input:          review.Input,
		AccessDuration: review.AccessDuration,
		Status:         review.Status,
		RevokeAt:       review.RevokeAt,
		ReviewOwner: types.ReviewOwner{
			Id:    reviewOwnerToStringFn("xt/id"),
			Name:  reviewOwnerToStringFn("user/name"),
			Email: reviewOwnerToStringFn("user/email"),
		},
		Connection: types.ReviewConnection{
			Id:   connectionToStringFn("xt/id"),
			Name: connectionToStringFn("connection/name"),
		},
		ReviewGroupsData: review.ReviewGroupsData,
	}

	return &reviewJSON
}
