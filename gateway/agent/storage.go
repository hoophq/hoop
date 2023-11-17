package agent

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) FindAll(ctx *user.Context) ([]Agent, error) {
	var res []pgrest.Agent
	if err := pgrest.New("/agents?org_id=eq.%v", ctx.Org.Id).
		List().
		DecodeInto(&res); err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	var agents []Agent
	for _, a := range res {
		agents = append(agents, Agent{a.ID, a.Token, a.OrgID, a.Name, a.Mode, a.GetMeta("hostname"), a.GetMeta("machine_id"),
			a.GetMeta("kernel_version"), a.GetMeta("version"),
			a.GetMeta("goversion"), a.GetMeta("compiler"),
			a.GetMeta("platform"), Status(a.Status)})
	}
	return agents, nil

	// var payload = `{:query {
	// 	:find [(pull ?agent [*])]
	// 	:in [org]
	// 	:where [[?agent :agent/org org]]}
	// 	:in-args ["` + context.Org.Id + `"]}`

	// b, err := s.Query([]byte(payload))
	// if err != nil {
	// 	return nil, err
	// }

	// var agents []Agent
	// if err := edn.Unmarshal(b, &agents); err != nil {
	// 	return nil, err
	// }

	// return agents, nil
}

func (s *Storage) FindByNameOrID(ctx *user.Context, nameOrID string) (*Agent, error) {
	client := pgrest.New("/agents?org_id=eq.%v&name=eq.%v", ctx.Org.Id, nameOrID)
	if _, err := uuid.Parse(nameOrID); err == nil {
		client = pgrest.New("/agents?org_id=eq.%v&id=eq.%v", ctx.Org.Id, nameOrID)
	}
	var a pgrest.Agent
	if err := client.FetchOne().DecodeInto(&a); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &Agent{a.ID, a.Token, a.OrgID, a.Name, a.Mode, a.GetMeta("hostname"), a.GetMeta("machine_id"),
		a.GetMeta("kernel_version"), a.GetMeta("version"),
		a.GetMeta("goversion"), a.GetMeta("compiler"),
		a.GetMeta("platform"), Status(a.Status)}, nil

	// payload := fmt.Sprintf(`{:query
	// 	{:find [(pull ?a [*])]
	// 	:in [org-id agentname agentid]
	// 	:where [[?a :agent/org org-id]
	// 			(or [?a :agent/name agentname]
	// 				[?a :xt/id agentid])]}
	// 	:in-args [%q, %q, %q]}`, ctx.Org.Id, agentName, agentID)
	// b, err := s.Query([]byte(payload))
	// if err != nil {
	// 	return nil, err
	// }

	// var agents []*Agent
	// if err := edn.Unmarshal(b, &agents); err != nil {
	// 	return nil, err
	// }

	// if len(agents) == 0 {
	// 	return nil, nil
	// }

	// return agents[0], nil

}

func (s *Storage) FindByToken(token string) (*Agent, error) {
	if token == "" {
		return nil, nil
	}
	var a pgrest.Agent
	if err := pgrest.New("/agents?token=eq.%v", token).
		FetchOne().
		DecodeInto(&a); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &Agent{a.ID, a.Token, a.OrgID, a.Name, a.Mode, a.GetMeta("hostname"), a.GetMeta("machine_id"),
		a.GetMeta("kernel_version"), a.GetMeta("version"),
		a.GetMeta("goversion"), a.GetMeta("compiler"),
		a.GetMeta("platform"), Status(a.Status)}, nil

	// payload := fmt.Sprintf(`{:query {
	// 	:find [(pull ?agent [*])]
	// 	:in [token]
	// 	:where [[?agent :agent/token token]]}
	//     :in-args [%q]}`, token)
	// b, err := s.Query([]byte(payload))
	// if err != nil {
	// 	return nil, err
	// }

	// var agents []*Agent
	// if err := edn.Unmarshal(b, &agents); err != nil {
	// 	return nil, err
	// }

	// if len(agents) == 0 {
	// 	return nil, nil
	// }

	// return agents[0], nil
}

func (s *Storage) Persist(a *Agent) (int64, error) {
	status := StatusDisconnected
	if a.Status != "" {
		status = a.Status
	}
	return 0, pgrest.New("/agents").Create(map[string]any{
		"id":     a.Id,
		"token":  a.Token,
		"org_id": a.OrgId,
		"name":   a.Name,
		"mode":   a.Mode,
		"status": status,
		"metadata": map[string]string{
			"hostname":       a.Hostname,
			"platform":       a.Platform,
			"goversion":      a.GoVersion,
			"version":        a.Version,
			"kernel_version": a.KernelVersion,
			"compiler":       a.Compiler,
			"machine_id":     a.MachineId,
		},
	}).Error()
	// agentPayload := st.EntityToMap(agent)

	// txId, err := s.PersistEntities([]map[string]any{agentPayload})
	// if err != nil {
	// 	return 0, err
	// }

	// return txId, nil
}

// TODO: add organization context?!
func (s *Storage) Evict(xtID string) error {
	return pgrest.New("/agents?id=eq.%v", xtID).Delete().Error()
	// _, err := s.Storage.SubmitEvictTx(xtID)
	// return err
}
