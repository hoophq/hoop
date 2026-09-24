package apiscim

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const scimOrgID = "00000000-0000-0000-0000-0000000000d1"

// The handler refuses outside the control plane, and appconfig.Load is one
// shot, so this test binary runs as a control plane.
func TestMain(m *testing.M) {
	os.Setenv("API_URL", "http://localhost:8009")
	if err := appconfig.Load(appconfig.AppModeControlPlane); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func startSCIM(t *testing.T) *httptest.Server {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })
	if err := modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatalf("open gorm connection: %v", err)
	}
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'scim-test')`, scimOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if err := models.ReplaceSCIMToken(models.DB, scimOrgID, models.HashAPIKey("secret-token"), "admin@example.com"); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := apiroutes.New(engine.Group("/api"))
	r.Any("/scim/v2/*path", r.SCIMAuthMiddleware, Handler)
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, srv *httptest.Server, method, path, body string, want int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+"/api/scim/v2"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Content-Type", "application/scim+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func memberGroups(t *testing.T, email string) []string {
	t.Helper()
	var names []string
	if err := models.DB.Raw(`
		SELECT ug.name FROM private.user_groups ug JOIN private.users u ON u.id = ug.user_id
		WHERE u.org_id = ? AND lower(u.email) = lower(?) ORDER BY ug.name`, scimOrgID, email).Scan(&names).Error; err != nil {
		t.Fatalf("list groups: %v", err)
	}
	return names
}

func TestSCIM(t *testing.T) {
	srv := startSCIM(t)

	t.Run("refuses a bad token", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/scim/v2/Users", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", resp.StatusCode)
		}
	})

	call(t, srv, http.MethodGet, "/ServiceProviderConfig", "", http.StatusOK)

	// Okta creates the user, then looks it up by userName.
	created := call(t, srv, http.MethodPost, "/Users", `{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
		"userName": "ana@example.com",
		"name": {"givenName": "Ana", "familyName": "Lima"},
		"emails": [{"primary": true, "value": "ana@example.com", "type": "work"}],
		"displayName": "Ana Lima",
		"externalId": "00u1",
		"active": true
	}`, http.StatusCreated)
	anaID, _ := created["id"].(string)
	if anaID == "" || created["externalId"] != "00u1" {
		t.Fatalf("created = %v", created)
	}
	list := call(t, srv, http.MethodGet, `/Users?filter=userName%20eq%20%22ana@example.com%22`, "", http.StatusOK)
	if list["totalResults"] != float64(1) {
		t.Fatalf("filter by userName: %v", list)
	}
	call(t, srv, http.MethodPost, "/Users", `{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
		"userName": "ANA@example.com",
		"emails": [{"primary": true, "value": "ana@example.com"}]
	}`, http.StatusConflict)

	// A second user, created by Entra ID.
	bob := call(t, srv, http.MethodPost, "/Users", `{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
		"userName": "bob@example.com",
		"externalId": "entra-2",
		"active": "True"
	}`, http.StatusCreated)
	bobID, _ := bob["id"].(string)

	// Okta creates the group with its members.
	group := call(t, srv, http.MethodPost, "/Groups", `{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:Group"],
		"displayName": "dba-leads",
		"members": [{"value": "`+anaID+`", "display": "ana@example.com"}]
	}`, http.StatusCreated)
	groupID, _ := group["id"].(string)
	if got := memberGroups(t, "ana@example.com"); !slices.Equal(got, []string{"dba-leads"}) {
		t.Fatalf("ana groups = %v", got)
	}

	// Okta's membership patch: remove by filter, add by list.
	call(t, srv, http.MethodPatch, "/Groups/"+groupID, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [
			{"op": "remove", "path": "members[value eq \"`+anaID+`\"]"},
			{"op": "add", "path": "members", "value": [{"value": "`+bobID+`"}]}
		]
	}`, http.StatusOK)
	if got := memberGroups(t, "ana@example.com"); len(got) != 0 {
		t.Fatalf("ana groups = %v; want none", got)
	}
	if got := memberGroups(t, "bob@example.com"); !slices.Equal(got, []string{"dba-leads"}) {
		t.Fatalf("bob groups = %v", got)
	}

	// Entra ID renames by path with a capitalized op.
	call(t, srv, http.MethodPatch, "/Groups/"+groupID, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "Replace", "path": "displayName", "value": "database-leads"}]
	}`, http.StatusOK)
	if got := memberGroups(t, "bob@example.com"); !slices.Equal(got, []string{"database-leads"}) {
		t.Fatalf("bob groups after rename = %v", got)
	}
	groups := call(t, srv, http.MethodGet, `/Groups?filter=displayName%20eq%20%22database-leads%22`, "", http.StatusOK)
	if groups["totalResults"] != float64(1) {
		t.Fatalf("filter by displayName: %v", groups)
	}

	// Okta deactivates with a value object and no path; Entra with "False".
	call(t, srv, http.MethodPatch, "/Users/"+bobID, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "replace", "value": {"active": false}}]
	}`, http.StatusOK)
	if got := memberGroups(t, "bob@example.com"); len(got) != 0 {
		t.Fatalf("deactivated bob holds %v", got)
	}
	if users, _ := models.ListApproverUsersByEmailAndOrg(models.DB, scimOrgID, "bob@example.com"); len(users) != 0 {
		t.Fatalf("bob is still active")
	}
	patched := call(t, srv, http.MethodPatch, "/Users/"+anaID, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [
			{"op": "Replace", "path": "emails[type eq \"work\"].value", "value": "ana.lima@example.com"},
			{"op": "Replace", "path": "active", "value": "False"}
		]
	}`, http.StatusOK)
	if patched["active"] != false {
		t.Fatalf("entra deactivation: %v", patched)
	}
	var email string
	models.DB.Raw(`SELECT email FROM private.users WHERE id = ?`, anaID).Scan(&email)
	if email != "ana.lima@example.com" {
		t.Fatalf("email = %q", email)
	}

	call(t, srv, http.MethodGet, "/Users/not-a-uuid", "", http.StatusNotFound)
	call(t, srv, http.MethodGet, "/Groups/not-a-uuid", "", http.StatusNotFound)
	call(t, srv, http.MethodDelete, "/Groups/"+groupID, "", http.StatusNoContent)
	call(t, srv, http.MethodGet, "/Groups/"+groupID, "", http.StatusNotFound)
	call(t, srv, http.MethodDelete, "/Users/"+anaID, "", http.StatusNoContent)
}
