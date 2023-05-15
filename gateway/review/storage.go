package review

import (
	"fmt"
	"time"

	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}

	XtdbReview struct {
		Id             string        `edn:"xt/id"`
		OrgId          string        `edn:"review/org"`
		Type           string        `edn:"review/type"`
		SessionId      string        `edn:"review/session"`
		ConnectionId   string        `edn:"review/connection"`
		CreatedBy      string        `edn:"review/created-by"`
		Input          string        `edn:"review/input"`
		AccessDuration time.Duration `edn:"review/access-duration"`
		RevokeAt       *time.Time    `edn:"review/revoke-at"`
		Status         Status        `edn:"review/status"`
		ReviewGroups   []string      `edn:"review/review-groups"`
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
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?r [:xt/id
						:review/type
						:review/status
						:review/access-duration
						:review/approved-at
						:review/input
						:review/session
						:review/connection
						:review/created-by
						{:review/created-by [:user/email]}
						{:review/connection [:connection/name]}])]
		:in [org]
		:where [[?r :review/org org]
				[?r :review/connection connid]
				[?c :xt/id connid]]}
		:in-args [%q]}`, context.Org.Id)

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

func (s *Storage) FindApprovedJitReviews(ctx *user.Context, connID string) (*Review, error) {
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?r [:xt/id
						:review/type
						:review/status
						:review/access-duration
						:review/revoke-at
						:review/input
						:review/session
						:review/connection
						:review/created-by
						{:review/created-by [:user/email]}
						{:review/connection [:connection/name]}])]
		:in [arg-orgid arg-userid arg-connid arg-status arg-now-date]
		:where [[?r :review/org arg-orgid]
				[?r :review/created-by arg-userid]
				[?r :review/connection arg-connid]
				[?r :review/status arg-status]
				[?c :xt/id arg-connid]
				[?r :review/revoke-at revoke-at]
				[(< arg-now-date revoke-at)]]}
		:in-args [%q %q %q %q #inst%q]}`,
		ctx.Org.Id, ctx.User.Id,
		connID, StatusApproved,
		time.Now().UTC().Format(time.RFC3339Nano))
	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var reviews []Review
	if err := edn.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}
	if len(reviews) == 0 {
		return nil, nil
	}
	// in case of multiple reviews approved
	// there shouldn't be a problem, return the first one
	return &reviews[0], nil
}

func (s *Storage) FindById(ctx *user.Context, id string) (*Review, error) {
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?r [:xt/id
						:review/type
						:review/status
						:review/access-duration
						:review/revoke-at
						:review/input
						:review/session
						:review/connection
						:review/created-by
							{:review/connection [:xt/id :connection/name]}
							{:review/review-groups [*
								{:review-group/reviewed-by [:xt/id :user/name :user/email]}]}
							{:review/created-by [:xt/id :user/name :user/email]}])]
		:in [org id]
		:where [[?r :review/org org]
				[?r :xt/id id]
				[?r :review/connection connid]
				[?c :xt/id connid]]}
        :in-args [%q %q]}`, ctx.Org.Id, id)

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
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?r [:xt/id
						:review/type
						:review/status
						:review/access-duration
						:review/revoke-at
						:review/input
						:review/session
						:review/connection
						:review/created-by
							{:review/connection [:xt/id :connection/name]}
							{:review/review-groups [*
								{:review-group/reviewed-by [:xt/id :user/name :user/email]}]}
							{:review/created-by [:xt/id :user/name :user/email]}])]
		:in [session-id]
		:where [[?r :review/session session-id]
				[?r :review/connection connid]
				[?c :xt/id connid]]}
        :in-args [%q]}`, sessionID)

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

func (s *Storage) Persist(ctx *user.Context, review *Review) (int64, error) {
	reviewGroupIds := make([]string, 0)

	var payloads []st.TxEdnStruct
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
		payloads = append(payloads, xg)
	}

	xtdbReview := &XtdbReview{
		Id:             review.Id,
		OrgId:          ctx.Org.Id,
		Type:           review.Type,
		SessionId:      review.Session,
		ConnectionId:   review.Connection.Id,
		CreatedBy:      ctx.User.Id,
		Input:          review.Input,
		AccessDuration: review.AccessDuration,
		RevokeAt:       review.RevokeAt,
		Status:         review.Status,
		ReviewGroups:   reviewGroupIds,
	}

	tx, err := s.SubmitPutTx(append(payloads, xtdbReview)...)
	if err != nil {
		return 0, err
	}

	return tx.TxID, nil
}
