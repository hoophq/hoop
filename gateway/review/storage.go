package review

import (
	"fmt"
	"strings"
	"time"

	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) FindAll(context *user.Context) ([]types.Review, error) {
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?r [:xt/id
						:review/type
						:review/created-at
						:review/status
						:review/access-duration
						:review/approved-at
						:review/input
						:review/session
						:review/connection
						:review/created-by
						{:review/created-by [:user/email :user/slack-id]}
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

	var reviews []types.Review
	if err := edn.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}

	return reviews, nil
}

func (s *Storage) FindApprovedJitReviews(ctx *user.Context, connID string) (*types.Review, error) {
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
						{:review/created-by [:user/email :user/slack-id]}
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
		connID, types.ReviewStatusApproved,
		time.Now().UTC().Format(time.RFC3339Nano))
	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var reviews []types.Review
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

func (s *Storage) FindById(ctx *user.Context, id string) (*types.Review, error) {
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
						:review/created-by
							{:review/connection [:xt/id :connection/name]}
							{:review/review-groups [*
								{:review-group/reviewed-by [:xt/id :user/name :user/email :user/slack-id]}]}
							{:review/created-by [:xt/id :user/name :user/email :user/slack-id]}])]
		:in [org id]
		:where [[?r :review/org org]
				[?r :xt/id id]
				[?r {:review/connection [:xt/id]} connid]
				[?c :xt/id connid]]}
        :in-args [%q %q]}`, ctx.Org.Id, id)

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var reviews []types.Review
	if err := edn.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}

	if len(reviews) == 0 {
		return nil, nil
	}

	return &reviews[0], nil
}

func (s *Storage) queryDecoder(query string, into any, args ...any) error {
	qs := fmt.Sprintf(query, args...)
	httpBody, err := s.QueryRaw([]byte(qs))
	if err != nil {
		return err
	}
	if strings.Contains(string(httpBody), ":xtdb.error") {
		return fmt.Errorf(string(httpBody))
	}
	return edn.Unmarshal(httpBody, into)
}

func (s *Storage) PersistSessionAsReady(sess *types.Session) (*st.TxResponse, error) {
	sess.Status = "ready"
	return s.SubmitPutTx(sess)
}

func (s *Storage) FindSessionBySessionId(sessionID string) (*types.Session, error) {
	var resultItems [][]types.Session
	err := s.queryDecoder(`
	{:query {
		:find [(pull ?s [*])]
		:in [session-id]
		:where [[?s :xt/id session-id]]}
	:in-args [%q]}`, &resultItems, sessionID)
	if err != nil {
		return nil, err
	}
	items := make([]types.Session, 0)
	for _, i := range resultItems {
		items = append(items, i[0])
	}
	if len(items) > 0 {
		session := items[0]
		nonIndexedStreams := session.NonIndexedStream["stream"]
		for _, i := range nonIndexedStreams {
			session.EventStream = append(session.EventStream, i)
		}
		return &session, nil
	}
	return nil, nil
}

func (s *Storage) FindBySessionID(sessionID string) (*types.Review, error) {
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
							{:review/created-by [:xt/id :user/name :user/email :user/slack-id]}])]
		:in [session-id]
		:where [[?r :review/session session-id]
				[?r :review/connection connid]
				[?c :xt/id connid]]}
        :in-args [%q]}`, sessionID)

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var reviews []types.Review
	if err := edn.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}

	if len(reviews) == 0 {
		return nil, nil
	}

	return &reviews[0], nil
}

func (s *Storage) Persist(ctx *user.Context, review *types.Review) (int64, error) {
	reviewGroups := make([]types.ReviewGroup, 0)

	var payloads []st.TxEdnStruct
	for _, r := range review.ReviewGroups {
		reviewGroups = append(reviewGroups, r)
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
		Id:             review.Id,
		CreatedAt:      review.CreatedAt,
		OrgId:          review.OrgId,
		Type:           review.Type,
		Session:        review.Session,
		Connection:     review.Connection,
		ConnectionId:   review.ConnectionId,
		CreatedBy:      review.CreatedBy,
		ReviewOwner:    review.ReviewOwner,
		Input:          review.Input,
		AccessDuration: review.AccessDuration,
		RevokeAt:       review.RevokeAt,
		Status:         review.Status,
		ReviewGroups:   review.ReviewGroups,
	}

	tx, err := s.SubmitPutTx(append(payloads, xtdbReview)...)
	if err != nil {
		return 0, err
	}

	return tx.TxID, nil
}
