package jit

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

	XtdbJit struct {
		Id           string        `edn:"xt/id"`
		OrgId        string        `edn:"jit/org"`
		SessionId    string        `edn:"jit/session"`
		ConnectionId string        `edn:"jit/connection"`
		CreatedBy    string        `edn:"jit/created-by"`
		Time         time.Duration `edn:"jit/time"`
		Status       Status        `edn:"jit/status"`
		JitGroups    []string      `edn:"jit/jit-groups"`
	}

	XtdbGroup struct {
		Id         string  `json:"id"          edn:"xt/id"`
		Group      string  `json:"group"       edn:"jit-group/group"`
		Status     Status  `json:"status"      edn:"jit-group/status"`
		ReviewedBy *string `json:"reviewed_by" edn:"jit-group/reviewed-by"`
		ReviewDate *string `json:"review_date" edn:"jit-group/review-date"`
	}
)

func (s *Storage) FindAll(context *user.Context) ([]Jit, error) {
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?jit [:xt/id
                           :jit/status
                           :jit/time
                           :jit/session
                           :jit/connection
                           :jit/created-by 
                           {:jit/created-by [:user/email]}
                           {:jit/connection [:connection/name]}])]
		:in [org]
		:where [[?jit :jit/org org]
				[?jit :jit/connection connid]
				[?c :xt/id connid]]}
		:in-args [%q]}`, context.Org.Id)

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var jits []Jit
	if err := edn.Unmarshal(b, &jits); err != nil {
		return nil, err
	}

	return jits, nil
}

func (s *Storage) FindById(context *user.Context, id string) (*Jit, error) {
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?jit [:xt/id
                           :jit/status
                           :jit/time
                           :jit/session
                           :jit/connection
                           :jit/created-by
                           {:jit/connection [:xt/id :connection/name]}
                           {:jit/jit-groups [*
                           {:jit-group/reviewed-by [:xt/id :user/name :user/email]}]}
                           {:jit/created-by [:xt/id :user/name :user/email]}])]
		:in [id, orgid]
		:where [[?jit :xt/id id]
				[?jit :jit/org orgid]
				[?jit :jit/connection connid]
				[?c :xt/id connid]]}
        :in-args [%q %q]}`, id, context.Org.Id)

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var jits []*Jit
	if err := edn.Unmarshal(b, &jits); err != nil {
		return nil, err
	}

	if len(jits) == 0 {
		return nil, nil
	}

	return jits[0], nil
}

func (s *Storage) FindBySessionID(sessionID string) (*Jit, error) {
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?jit [:xt/id
                           :jit/status
                           :jit/time
                           :jit/session
                           :jit/connection
                           :jit/created-by
                           {:jit/connection [:xt/id :connection/name]}
                           {:jit/jit-groups [*
                           		{:jit-group/reviewed-by [:xt/id :user/name :user/email]}]}
                           {:jit/created-by [:xt/id :user/name :user/email]}])]
		:in [sessionID]
		:where [[?jit :jit/session sessionID]
				[?jit :jit/connection connid]
				[?c :xt/id connid]]}
        :in-args [%q]}`, sessionID)

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var jits []*Jit
	if err := edn.Unmarshal(b, &jits); err != nil {
		return nil, err
	}

	if len(jits) == 0 {
		return nil, nil
	}

	return jits[0], nil
}

func (s *Storage) Persist(context *user.Context, jit *Jit) (int64, error) {
	jitGroupIds := make([]string, 0)
	payload := make([]map[string]any, 0)

	for _, r := range jit.JitGroups {
		jitGroupIds = append(jitGroupIds, r.Id)
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

	xtdbJit := &XtdbJit{
		Id:           jit.Id,
		OrgId:        context.Org.Id,
		SessionId:    jit.Session,
		ConnectionId: jit.Connection.Id,
		CreatedBy:    context.User.Id,
		Time:         jit.Time,
		Status:       jit.Status,
		JitGroups:    jitGroupIds,
	}

	jitPayload := st.EntityToMap(xtdbJit)
	payload = append(payload, jitPayload)

	txId, err := s.PersistEntities(payload)
	if err != nil {
		return 0, err
	}

	return txId, nil
}
