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

	ReviewXtdb struct {
		Id           string   `json:"id"            edn:"xt/id"`
		OrgId        string   `json:"-"             edn:"review/org"`
		CreatedById  string   `json:"-"             edn:"review/created-by"`
		Script       string   `json:"script"        edn:"review/script"`
		Status       Status   `json:"status"        edn:"review/status"`
		ReviewGroups []string `json:"review_groups" edn:"review/review-groups"`
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

func (s *Storage) Persist(review *Review) (int64, error) {
	reviewPayload := st.EntityToMap(review)

	txId, err := s.PersistEntities([]map[string]any{reviewPayload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}
