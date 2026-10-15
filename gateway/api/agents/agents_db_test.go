package apiagents_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	apiagents "github.com/hoophq/hoop/gateway/api/agents"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const testOrgID = "00000000-0000-0000-0000-0000000000d1"

func startTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	sqlDB, err := models.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'agent-tags-test')`, testOrgID).Error)
}

// call runs handler as an admin of the test org.
func call(t *testing.T, handler gin.HandlerFunc, method, target, body string, params ...gin.Param) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin@hoop.dev", testOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin}).
		WithGrpcURL("grpc://127.0.0.1:8010"))
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	handler(c)
	// gin writes a status without a body only after the handler chain ends.
	c.Writer.WriteHeaderNow()
	return rec
}

func nameParam(name string) gin.Param { return gin.Param{Key: "nameOrID", Value: name} }

func getAgent(t *testing.T, name string) openapi.AgentResponse {
	t.Helper()
	rec := call(t, apiagents.Get, http.MethodGet, "/api/agents/"+name, "", nameParam(name))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp openapi.AgentResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func listAgents(t *testing.T, query string) (int, []openapi.AgentResponse) {
	t.Helper()
	rec := call(t, apiagents.List, http.MethodGet, "/api/agents?"+query, "")
	var items []openapi.AgentResponse
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &items))
	}
	return rec.Code, items
}

func names(items []openapi.AgentResponse) []string {
	var out []string
	for _, a := range items {
		out = append(out, a.Name)
	}
	return out
}

func TestAgentTagsAPI(t *testing.T) {
	startTestDB(t)
	const env = "hoop.dev/infrastructure.environment"

	t.Run("create without tags works as before and returns tags as an empty object", func(t *testing.T) {
		rec := call(t, apiagents.Post, http.MethodPost, "/api/agents", `{"name":"agent-bare"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

		rec = call(t, apiagents.Get, http.MethodGet, "/api/agents/agent-bare", "", nameParam("agent-bare"))
		require.Equal(t, http.StatusOK, rec.Code)
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		assert.JSONEq(t, `{}`, string(raw["tags"]))
	})

	t.Run("create with tags stores them", func(t *testing.T) {
		rec := call(t, apiagents.Post, http.MethodPost, "/api/agents",
			`{"name":"agent-prod","tags":{"`+env+`":"prod","business-unit":"payments"}}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		rec = call(t, apiagents.Post, http.MethodPost, "/api/agents",
			`{"name":"agent-stg","tags":{"`+env+`":"staging"}}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

		assert.Equal(t, map[string]string{env: "prod", "business-unit": "payments"}, getAgent(t, "agent-prod").Tags)
	})

	t.Run("create with invalid tags returns 400 and creates nothing", func(t *testing.T) {
		tooMany := map[string]string{}
		for _, k := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10", "a11"} {
			tooMany[k] = "v"
		}
		body, _ := json.Marshal(map[string]any{"name": "agent-bad", "tags": tooMany})
		for _, payload := range []string{
			`{"name":"agent-bad","tags":{"bad key!":"v"}}`,
			`{"name":"agent-bad","tags":{"team":""}}`,
			`{"name":"agent-bad","tags":{"team":"bad#value"}}`,
			string(body),
		} {
			rec := call(t, apiagents.Post, http.MethodPost, "/api/agents", payload)
			assert.Equal(t, http.StatusBadRequest, rec.Code, payload)
		}
		rec := call(t, apiagents.Get, http.MethodGet, "/api/agents/agent-bad", "", nameParam("agent-bad"))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("list returns the tags of each agent, never null", func(t *testing.T) {
		code, items := listAgents(t, "")
		require.Equal(t, http.StatusOK, code)
		byName := map[string]openapi.AgentResponse{}
		for _, a := range items {
			byName[a.Name] = a
		}
		assert.Equal(t, map[string]string{env: "staging"}, byName["agent-stg"].Tags)
		assert.NotNil(t, byName["agent-bare"].Tags)
		assert.Empty(t, byName["agent-bare"].Tags)
	})

	t.Run("list filters by tag_selector and combines with status", func(t *testing.T) {
		code, items := listAgents(t, "tag_selector="+env+"=prod")
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, []string{"agent-prod"}, names(items))

		code, items = listAgents(t, "tag_selector="+env+"!=prod")
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, []string{"agent-stg"}, names(items))

		code, items = listAgents(t, "status=CONNECTED&tag_selector="+env+"=prod")
		require.Equal(t, http.StatusOK, code)
		assert.Empty(t, items)

		code, items = listAgents(t, "status=DISCONNECTED&tag_selector="+env+"=prod")
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, []string{"agent-prod"}, names(items))

		code, _ = listAgents(t, "tag_selector=env")
		assert.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("put replaces the tags and changes no other field", func(t *testing.T) {
		before := getAgent(t, "agent-stg")
		rec := call(t, apiagents.UpdateTags, http.MethodPut, "/api/agents/agent-stg/tags",
			`{"`+env+`":"prod","team":"data"}`, nameParam("agent-stg"))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var resp openapi.AgentResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, map[string]string{env: "prod", "team": "data"}, resp.Tags)

		after := getAgent(t, "agent-stg")
		assert.Equal(t, map[string]string{env: "prod", "team": "data"}, after.Tags)
		after.Tags = before.Tags
		assert.Equal(t, before, after)
	})

	t.Run("put by ID works", func(t *testing.T) {
		id := getAgent(t, "agent-stg").ID
		rec := call(t, apiagents.UpdateTags, http.MethodPut, "/api/agents/"+id+"/tags",
			`{"team":"data"}`, nameParam(id))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, map[string]string{"team": "data"}, getAgent(t, "agent-stg").Tags)
	})

	t.Run("put with an empty object removes all tags", func(t *testing.T) {
		rec := call(t, apiagents.UpdateTags, http.MethodPut, "/api/agents/agent-stg/tags", `{}`, nameParam("agent-stg"))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		assert.JSONEq(t, `{}`, string(raw["tags"]))
		assert.Empty(t, getAgent(t, "agent-stg").Tags)
	})

	t.Run("put on an unknown agent returns 404", func(t *testing.T) {
		rec := call(t, apiagents.UpdateTags, http.MethodPut, "/api/agents/nope/tags", `{"team":"data"}`, nameParam("nope"))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("put with an invalid body returns 400 and keeps the tags", func(t *testing.T) {
		for _, payload := range []string{`null`, `[]`, `{"team":1}`, `{"bad key!":"v"}`, ``} {
			rec := call(t, apiagents.UpdateTags, http.MethodPut, "/api/agents/agent-prod/tags", payload, nameParam("agent-prod"))
			assert.Equal(t, http.StatusBadRequest, rec.Code, payload)
		}
		assert.Equal(t, "payments", getAgent(t, "agent-prod").Tags["business-unit"])
	})

	t.Run("agent-tags returns predefined and in use tags", func(t *testing.T) {
		rec := call(t, apiagents.ListTags, http.MethodGet, "/api/agent-tags", "")
		require.Equal(t, http.StatusOK, rec.Code)
		var list openapi.AgentTagList
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
		assert.Contains(t, list.Items, openapi.AgentTag{Key: env, Value: "sandbox"})
		assert.Contains(t, list.Items, openapi.AgentTag{Key: "business-unit", Value: "payments"})

		// A custom tag leaves the suggestions when no agent uses it.
		rec = call(t, apiagents.UpdateTags, http.MethodPut, "/api/agents/agent-prod/tags", `{}`, nameParam("agent-prod"))
		require.Equal(t, http.StatusOK, rec.Code)
		rec = call(t, apiagents.ListTags, http.MethodGet, "/api/agent-tags", "")
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
		assert.NotContains(t, list.Items, openapi.AgentTag{Key: "business-unit", Value: "payments"})
		assert.Contains(t, list.Items, openapi.AgentTag{Key: env, Value: "prod"})
	})

	t.Run("delete removes the tags and a recreated agent starts with none", func(t *testing.T) {
		rec := call(t, apiagents.UpdateTags, http.MethodPut, "/api/agents/agent-stg/tags", `{"team":"data"}`, nameParam("agent-stg"))
		require.Equal(t, http.StatusOK, rec.Code)
		id := getAgent(t, "agent-stg").ID

		rec = call(t, apiagents.Delete, http.MethodDelete, "/api/agents/agent-stg", "", nameParam("agent-stg"))
		require.Equal(t, http.StatusNoContent, rec.Code)
		var count int64
		require.NoError(t, models.DB.Raw(`SELECT COUNT(*) FROM private.agent_tags WHERE agent_id = ?`, id).Scan(&count).Error)
		assert.Zero(t, count)

		rec = call(t, apiagents.Post, http.MethodPost, "/api/agents", `{"name":"agent-stg"}`)
		require.Equal(t, http.StatusCreated, rec.Code)
		again := getAgent(t, "agent-stg")
		assert.Equal(t, id, again.ID)
		assert.Empty(t, again.Tags)
	})
}
