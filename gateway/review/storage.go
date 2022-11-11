package review

import (
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}

	XtdbReview struct {
		Id           string   `edn:"xt/id"`
		OrgId        string   `edn:"review/org"`
		CreatedBy    string   `edn:"review/created-by"`
		Command      string   `edn:"review/command"`
		Status       Status   `edn:"review/status"`
		ReviewGroups []string `edn:"review/review-groups"`
	}
)

func (s *Storage) FindAll(context *user.Context) ([]Review, error) {
	var payload = `{:query {
		:find [(pull ?review [*])] 
		:in [org]
		:where [[?review :review/org org]]}
		:in-args ["` + context.Org.Id + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var reviews []Review
	if err := edn.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}

	return reviews, nil
}

func (s *Storage) FindById(id string) (*Review, error) {
	maybeReview, err := s.GetEntity(id)
	if err != nil {
		return nil, err
	}

	if maybeReview == nil {
		return nil, nil
	}

	var review Review
	if err := edn.Unmarshal(maybeReview, &review); err != nil {
		return nil, err
	}

	return &review, nil
}

func (s *Storage) Persist(context *user.Context, review *Review) (int64, error) {
	reviewGroupIds := make([]string, 0)
	payload := make([]map[string]any, 0)

	for _, r := range review.ReviewGroups {
		reviewGroupIds = append(reviewGroupIds, r.Id)
		rp := st.EntityToMap(r)
		payload = append(payload, rp)
	}

	xtdbReview := &XtdbReview{
		Id:           review.Id,
		OrgId:        context.Org.Id,
		CreatedBy:    context.User.Id,
		Command:      review.Command,
		Status:       review.Status,
		ReviewGroups: reviewGroupIds,
	}

	reviewPayload := st.EntityToMap(xtdbReview)
	payload = append(payload, reviewPayload)

	txId, err := s.PersistEntities(payload)
	if err != nil {
		return 0, err
	}

	return txId, nil
}
