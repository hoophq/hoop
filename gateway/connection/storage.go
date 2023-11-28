package connection

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}

	xtdb struct {
		Id             string         `edn:"xt/id"`
		OrgId          string         `edn:"connection/org"`
		Name           string         `edn:"connection/name"`
		IconName       string         `edn:"connection/icon-name"`
		Command        []string       `edn:"connection/command"`
		Type           Type           `edn:"connection/type"`
		SecretProvider SecretProvider `edn:"connection/secret-provider"`
		SecretId       string         `edn:"connection/secret"`
		CreatedById    string         `edn:"connection/created-by"`
		AgentId        string         `edn:"connection/agent"`
	}
)

var errNotFound = errors.New("not found")

func (s *Storage) Persist(context *user.Context, c *Connection) (int64, error) {
	if pgrest.Rollout {
		envs := map[string]string{}
		for key, val := range c.Secret {
			envs[key] = fmt.Sprintf("%v", val)
		}
		return 0, pgconnections.New().Create(context, pgrest.Connection{
			ID:            c.Id,
			OrgID:         context.Org.Id,
			AgentID:       c.AgentId,
			LegacyAgentID: c.AgentId,
			Name:          c.Name,
			Command:       c.Command,
			Type:          string(c.Type),
			Envs:          envs,
		})
	}
	secretId := uuid.New().String()

	conn := xtdb{
		Id:             c.Id,
		OrgId:          context.Org.Id,
		Name:           c.Name,
		IconName:       c.IconName,
		Command:        c.Command,
		Type:           c.Type,
		SecretProvider: c.SecretProvider,
		SecretId:       secretId,
		CreatedById:    context.User.Id,
		AgentId:        c.AgentId,
	}

	connectionPayload := st.EntityToMap(&conn)
	secretPayload := buildSecretMap(c.Secret, secretId)

	entities := []map[string]any{secretPayload, connectionPayload}
	txId, err := s.PersistEntities(entities)
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) Evict(ctx *user.Context, connectionName string) error {
	if pgrest.Rollout {
		return pgconnections.New().Delete(ctx, connectionName)
	}
	pluginQuery := fmt.Sprintf(`{:query {
		:find [xtid connid]
		:in [orgid name]
		:where [
            [?p :connection/name name]
            [?p :connection/org orgid]
            [?p :xt/id connid]
            [?c :plugin-connection/id connid]
            [?c :xt/id xtid]]}
	:in-args [%q %q]}`, ctx.Org.Id, connectionName)
	data, err := s.QueryRaw([]byte(pluginQuery))
	if err != nil {
		return fmt.Errorf("failed fetching connection plugin, err=%v", err)
	}
	var ednResp [][]string
	if err := edn.Unmarshal(data, &ednResp); err != nil {
		return fmt.Errorf("failed decoding result, err=%v", err)
	}

	var evictList []string
	var connID string
	for _, objList := range ednResp {
		if len(objList) != 2 {
			return fmt.Errorf("wrong response structure, want=2, got=%v", len(objList))
		}
		// plugin-connection xt/id's
		evictList = append(evictList, objList[0])
		if connID == "" {
			connID = objList[1]
		}
	}
	if len(evictList) == 0 {
		// the connection may still exists but there isn't any
		// plugin active. It's safe to evict the connection
		conn, err := s.FindOne(ctx, connectionName)
		if err != nil {
			return err
		}
		if conn == nil {
			return errNotFound
		}
		connID = conn.Id
	}
	// delete the connection last
	evictList = append(evictList, connID)
	tx, err := s.SubmitEvictTx(evictList...)
	if err != nil {
		return err
	}
	log.Infof("org=%v, user=%v, tx=%v, evicted connection %s",
		ctx.Org.Id, ctx.User.Email, fmt.Sprintf("%v", tx.TxID), connectionName)
	return nil
}

func (s *Storage) FindAll(context *user.Context) ([]BaseConnection, error) {
	if pgrest.Rollout {
		items, err := pgconnections.New().FetchAll(context)
		if err != nil {
			return nil, err
		}
		var connections []BaseConnection
		for _, c := range items {
			if c.LegacyAgentID != "" {
				c.AgentID = c.LegacyAgentID
			}
			connections = append(connections, BaseConnection{
				c.ID, c.Name, "", c.Command, Type(c.Type), c.AgentID, DBSecretProvider,
			})
		}
		return connections, nil
	}

	var payload = `{:query {
		:find [(pull ?connection [*])] 
		:in [org]
		:where [[?connection :connection/org org]]}
		:in-args ["` + context.Org.Id + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var connections []BaseConnection
	if err := edn.Unmarshal(b, &connections); err != nil {
		return nil, err
	}

	return connections, nil
}

func (s *Storage) FindOne(context *user.Context, nameOrID string) (*Connection, error) {
	if pgrest.Rollout {
		conn, err := pgconnections.New().FetchOneByNameOrID(context, nameOrID)
		if err != nil {
			return nil, err
		}
		if conn == nil {
			return nil, nil
		}
		secrets := Secret{}
		for key, val := range conn.Envs {
			secrets[key] = val
		}
		return &Connection{
			BaseConnection: BaseConnection{
				Id:             conn.ID,
				Name:           conn.Name,
				IconName:       "",
				Command:        conn.Command,
				Type:           Type(conn.Type),
				AgentId:        conn.AgentID,
				SecretProvider: DBSecretProvider},
			Secret: secrets,
		}, nil
	}
	connectionID, connectionName := "", nameOrID
	if _, err := uuid.Parse(nameOrID); err == nil {
		connectionID = nameOrID
		connectionName = ""
	}
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?c [*])]
		:in [org-id connection-name connection-id]
		:where [[?c :connection/org org]
				(or [?c :connection/name connection-name]
					[?c :xt/id connection-id])]}
		:in-args [%q %q %q]}`, context.Org.Id, connectionName, connectionID)

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var connections []xtdb
	if err := edn.Unmarshal(b, &connections); err != nil {
		return nil, err
	}

	if len(connections) == 0 {
		return nil, nil
	}

	conn := connections[0]
	secret, err := s.getSecret(conn.SecretId)
	if err != nil {
		return nil, err
	}
	return &Connection{
		BaseConnection: BaseConnection{
			Id:             conn.Id,
			Name:           conn.Name,
			IconName:       conn.IconName,
			Command:        conn.Command,
			Type:           conn.Type,
			SecretProvider: conn.SecretProvider,
			AgentId:        conn.AgentId,
		},
		Secret: secret,
	}, nil
}

func (s *Storage) getSecret(secretId string) (Secret, error) {
	var payload = `{:query {
		:find [(pull ?secret [*])]
		:in [id]
		:where [[?secret :xt/id id]]}
		:in-args ["` + secretId + `"]}`

	b, err := s.QueryAsJson([]byte(payload))
	if err != nil {
		return nil, err
	}

	var secrets []Secret
	if err := json.Unmarshal(b, &secrets); err != nil {
		return nil, err
	}

	if len(secrets) == 0 {
		return make(map[string]any), nil
	}

	sanitizedSecrets := removeSecretsPrefix(secrets[0])

	return sanitizedSecrets, nil
}

func buildSecretMap(secrets map[string]any, xtId string) map[string]any {
	secretPayload := map[string]any{
		"xt/id": xtId,
	}

	for key, value := range secrets {
		secretPayload[fmt.Sprintf("secret/%s", key)] = value
	}

	return secretPayload
}

func removeSecretsPrefix(secret map[string]any) map[string]any {
	n := make(map[string]any)
	for k, v := range secret {
		if strings.HasPrefix(k, "xt/id") {
			continue
		}
		n[k[7:]] = v
	}
	return n
}
