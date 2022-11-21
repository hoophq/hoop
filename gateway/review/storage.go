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
		SessionId    string   `edn:"review/session"`
		ConnectionId string   `edn:"review/connection"`
		CreatedBy    string   `edn:"review/created-by"`
		Command      string   `edn:"review/command"`
		Status       Status   `edn:"review/status"`
		ReviewGroups []string `edn:"review/review-groups"`
	}

	XtdbGroup struct {
		Id         string  `json:"id"          edn:"xt/id"`
		Group      string  `json:"group"       edn:"review-group/group"`
		Status     Status  `json:"status"      edn:"review-group/status"`
		ReviewedBy *string `json:"reviewed_by" edn:"review-group/reviewed-by"`
		ReviewDate *string `json:"review_date" edn:"review-group/review_date"`
	}
)

func (s *Storage) FindAll(context *user.Context) ([]Review, error) {
	var payload = `{:query {
		:find [(pull ?review [:xt/id
                              :review/status
		                      :review/command
                              :review/session
		                      :review/connection
		                      :review/created-by 
		                      {:review/created-by [:user/email]}
                              {:review/connection [:connection/name]}])]
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

func (s *Storage) FindById(context *user.Context, id string) (*Review, error) {
	var payload = `{:query {
		:find [(pull ?review [:xt/id
                              :review/status
							  :review/command
							  :review/session
							  :review/connection
							  :review/created-by
                                 {:review/connection [:xt/id :connection/name]}
                                 {:review/review-groups [*
                                     {:review-group/reviewed-by [:xt/id :user/name :user/email]}]}
                                 {:review/created-by [:xt/id :user/name :user/email]}])]
		:in [id org]
		:where [[?review :xt/id id]
                [?review :review/org org]]}
        :in-args ["` + id + `" "` + context.Org.Id + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var reviews []*Review
	if err := edn.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}

	if len(reviews) == 0 {
		return nil, nil
	}

	return reviews[0], nil
}

func (s *Storage) FindBySessionID(sessionID string) (*Review, error) {
	var payload = `{:query {
		:find [(pull ?review [:xt/id
                              :review/status
							  :review/command
							  :review/session
							  :review/connection
							  :review/created-by
                                 {:review/connection [:xt/id :connection/name]}
                                 {:review/review-groups [*
                                     {:review-group/reviewed-by [:xt/id :user/name :user/email]}]}
                                 {:review/created-by [:xt/id :user/name :user/email]}])]
		:in [sessionID]
		:where [[?review :review/session sessionID]]}
        :in-args ["` + sessionID + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var reviews []*Review
	if err := edn.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}

	if len(reviews) == 0 {
		return nil, nil
	}

	return reviews[0], nil
}

func (s *Storage) Persist(context *user.Context, review *Review) (int64, error) {
	reviewGroupIds := make([]string, 0)
	payload := make([]map[string]any, 0)

	for _, r := range review.ReviewGroups {
		reviewGroupIds = append(reviewGroupIds, r.Id)
		xg := &XtdbGroup{
			Id:         r.Id,
			Group:      r.Group,
			Status:     r.Status,
			ReviewDate: r.ReviewDate,
		}
		if r.ReviewedBy != nil {
			xg.ReviewedBy = &r.ReviewedBy.Id
		}
		rp := st.EntityToMap(xg)
		payload = append(payload, rp)
	}

	xtdbReview := &XtdbReview{
		Id:           review.Id,
		OrgId:        context.Org.Id,
		SessionId:    review.Session,
		ConnectionId: review.Connection.Id,
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
