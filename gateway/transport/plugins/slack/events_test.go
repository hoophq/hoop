package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/slack-go/slack"
)

const (
	approverOrgID = "00000000-0000-0000-0000-0000000000e1"
	botTeamID     = "T1"
)

func TestCheckSlackUser(t *testing.T) {
	ok := slackservice.SlackUser{ID: "U1", TeamID: botTeamID, Email: "ana@example.com", IsEmailConfirmed: true}
	for _, tt := range []struct {
		name   string
		mutate func(u *slackservice.SlackUser)
		want   string
	}{
		{name: "vouched", mutate: func(u *slackservice.SlackUser) {}},
		{name: "deactivated", mutate: func(u *slackservice.SlackUser) { u.Deleted = true }, want: cpDeactivatedMsg},
		{name: "bot", mutate: func(u *slackservice.SlackUser) { u.IsBot = true }, want: cpBotMsg},
		{name: "deactivated without email is still deactivated", mutate: func(u *slackservice.SlackUser) { u.Deleted = true; u.Email = "" }, want: cpDeactivatedMsg},
		{name: "guest without email is still a guest", mutate: func(u *slackservice.SlackUser) { u.IsRestricted = true; u.Email = "" }, want: cpGuestMsg},
		{name: "single channel guest", mutate: func(u *slackservice.SlackUser) { u.IsUltraRestricted = true }, want: cpGuestMsg},
		{name: "stranger", mutate: func(u *slackservice.SlackUser) { u.IsStranger = true }, want: cpOtherWorkspaceMsg},
		{name: "other workspace", mutate: func(u *slackservice.SlackUser) { u.TeamID = "T2" }, want: cpOtherWorkspaceMsg},
		// No email is what Slack answers when the app lacks users:read.email.
		{name: "no email", mutate: func(u *slackservice.SlackUser) { u.Email = "" }, want: cpNotLinkedMsg},
		{name: "unconfirmed email", mutate: func(u *slackservice.SlackUser) { u.IsEmailConfirmed = false }, want: cpUnconfirmedMsg},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := ok
			tt.mutate(&u)
			if got := checkSlackUser(&u, botTeamID, ""); got != tt.want {
				t.Errorf("checkSlackUser = %q, want %q", got, tt.want)
			}
		})
	}
}

// Enterprise Grid: a user whose home workspace is another one of the same grid
// org is the same company; one from another grid is not.
func TestSameWorkspace(t *testing.T) {
	for _, tt := range []struct {
		name               string
		userTeam, userGrid string
		botTeam, botGrid   string
		want               bool
	}{
		{"same team", "T1", "", "T1", "", true},
		{"other team, no grid", "T2", "", "T1", "", false},
		{"other team, same grid", "T2", "E1", "T1", "E1", true},
		{"other team, other grid", "T2", "E2", "T1", "E1", false},
		{"bot team unknown", "T2", "", "", "", true},
	} {
		u := &slackservice.SlackUser{TeamID: tt.userTeam, EnterpriseID: tt.userGrid}
		if got := sameWorkspace(u, tt.botTeam, tt.botGrid); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestPickApprover(t *testing.T) {
	if _, refusal := pickApprover(nil, "ana@example.com"); !strings.Contains(refusal, "No Hoop user") {
		t.Errorf("no user: %q", refusal)
	}
	one := []models.User{{ID: "u1"}}
	if u, refusal := pickApprover(one, "ana@example.com"); refusal != "" || u.ID != "u1" {
		t.Errorf("one user: %v %q", u, refusal)
	}
	two := []models.User{{ID: "u1"}, {ID: "u2"}}
	if _, refusal := pickApprover(two, "ana@example.com"); !strings.Contains(refusal, "More than one") {
		t.Errorf("two users: %q", refusal)
	}
}

// fakeSlack answers users.info from a table and records every ephemeral
// message, which is how a refused click reaches the Slack user.
type fakeSlack struct {
	mu         sync.Mutex
	users      map[string]map[string]any
	errors     map[string]string
	broken     map[string]bool
	ephemerals []string
}

func (f *fakeSlack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/users.info"):
		id := r.Form.Get("user")
		if f.broken[id] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if code, ok := f.errors[id]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": f.users[id]})
	case strings.HasSuffix(r.URL.Path, "/chat.postEphemeral"):
		f.mu.Lock()
		f.ephemerals = append(f.ephemerals, r.Form.Get("text"))
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message_ts": "1"})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeSlack) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ephemerals) == 0 {
		return ""
	}
	return f.ephemerals[len(f.ephemerals)-1]
}

func slackUserJSON(id, team, email string, mutate func(map[string]any)) map[string]any {
	u := map[string]any{
		"id": id, "team_id": team, "is_email_confirmed": true,
		"profile": map[string]any{"email": email},
	}
	if mutate != nil {
		mutate(u)
	}
	return u
}

func startApproverDB(t *testing.T) {
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
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'approver-test')`, approverOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

func seedApprover(t *testing.T, email, status, slackID string, groups ...string) string {
	t.Helper()
	id := uuid.NewString()
	var sid any
	if slackID != "" {
		sid = slackID
	}
	if err := models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status, slack_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, approverOrgID, "sub|"+id, email, email, status, sid).Error; err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	for _, g := range groups {
		if err := models.InsertUserGroups([]models.UserGroup{{OrgID: approverOrgID, UserID: id, Name: g}}); err != nil {
			t.Fatalf("seed group %s: %v", g, err)
		}
	}
	return id
}

func newFakeSlackService(t *testing.T, f *fakeSlack) *slackservice.SlackService {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return slackservice.NewWithAPIClient(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), botTeamID, "")
}

func clickEvent(ss *slackservice.SlackService, slackID, group string) *event {
	return &event{
		ss:    ss,
		orgID: approverOrgID,
		msg:   &slackservice.MessageReviewResponse{ID: "rev-1", SessionID: "sid-1", SlackID: slackID, GroupName: group},
	}
}

func TestResolveControlPlaneApprover(t *testing.T) {
	startApproverDB(t)
	seedApprover(t, "linked@corp.com", "active", "U-LINK-ACTIVE", "dba")
	seedApprover(t, "invited@corp.com", "invited", "U-LINK-INVITED", "dba")
	seedApprover(t, "gone@corp.com", "inactive", "U-LINK-INACTIVE", "dba")
	seedApprover(t, "left@corp.com", "active", "U-LINK-DELETED", "dba")
	seedApprover(t, "guest@corp.com", "active", "U-LINK-GUEST", "dba")
	seedApprover(t, "scope@corp.com", "active", "U-LINK-SCOPE", "dba")
	seedApprover(t, "noemail@corp.com", "active", "U-LINK-NOEMAIL", "dba")
	seedApprover(t, "email@corp.com", "active", "", "dba")
	seedApprover(t, "pending@corp.com", "invited", "", "dba")
	seedApprover(t, "dup@corp.com", "active", "", "dba")
	seedApprover(t, "DUP@corp.com", "active", "", "dba")
	seedApprover(t, "nogroup@corp.com", "active", "")

	f := &fakeSlack{
		users: map[string]map[string]any{
			"U-LINK-ACTIVE":   slackUserJSON("U-LINK-ACTIVE", botTeamID, "linked@corp.com", nil),
			"U-LINK-INVITED":  slackUserJSON("U-LINK-INVITED", botTeamID, "invited@corp.com", nil),
			"U-LINK-INACTIVE": slackUserJSON("U-LINK-INACTIVE", botTeamID, "gone@corp.com", nil),
			"U-LINK-DELETED":  slackUserJSON("U-LINK-DELETED", botTeamID, "left@corp.com", func(u map[string]any) { u["deleted"] = true }),
			"U-LINK-GUEST":    slackUserJSON("U-LINK-GUEST", botTeamID, "guest@corp.com", func(u map[string]any) { u["is_restricted"] = true }),
			// A linked user needs no email: the app may lack users:read.email.
			"U-LINK-NOEMAIL": slackUserJSON("U-LINK-NOEMAIL", botTeamID, "", nil),
			"U-EMAIL":        slackUserJSON("U-EMAIL", botTeamID, "Email@Corp.com", nil),
			"U-PENDING":      slackUserJSON("U-PENDING", botTeamID, "pending@corp.com", nil),
			"U-STRANGER":     slackUserJSON("U-STRANGER", "T9", "email@corp.com", func(u map[string]any) { u["is_stranger"] = true }),
			"U-OTHERTEAM":    slackUserJSON("U-OTHERTEAM", "T2", "email@corp.com", nil),
			"U-GUEST":        slackUserJSON("U-GUEST", botTeamID, "email@corp.com", func(u map[string]any) { u["is_restricted"] = true }),
			"U-NOEMAIL":      slackUserJSON("U-NOEMAIL", botTeamID, "", nil),
			"U-NOBODY":       slackUserJSON("U-NOBODY", botTeamID, "nobody@corp.com", nil),
			"U-DUP":          slackUserJSON("U-DUP", botTeamID, "dup@corp.com", nil),
			"U-NOGROUP":      slackUserJSON("U-NOGROUP", botTeamID, "nogroup@corp.com", nil),
			"U-OTHERGROUP":   slackUserJSON("U-OTHERGROUP", botTeamID, "email@corp.com", nil),
		},
		errors: map[string]string{"U-SCOPE": "missing_scope", "U-LINK-SCOPE": "missing_scope"},
		broken: map[string]bool{"U-APIERR": true},
	}
	ss := newFakeSlackService(t, f)
	p := &slackPlugin{apiURL: "http://localhost:8009"}

	for _, tt := range []struct {
		name      string
		slackID   string
		group     string
		wantEmail string
		wantMsg   string
	}{
		{name: "linked slack id, active", slackID: "U-LINK-ACTIVE", group: "dba", wantEmail: "linked@corp.com"},
		{name: "linked slack id, invited", slackID: "U-LINK-INVITED", group: "dba", wantEmail: "invited@corp.com"},
		{name: "linked slack id, inactive", slackID: "U-LINK-INACTIVE", group: "dba", wantMsg: cpInactiveMsg},
		{name: "linked slack id, deactivated in slack", slackID: "U-LINK-DELETED", group: "dba", wantMsg: cpDeactivatedMsg},
		{name: "linked slack id, now a guest", slackID: "U-LINK-GUEST", group: "dba", wantMsg: cpGuestMsg},
		{name: "linked slack id, missing users:read", slackID: "U-LINK-SCOPE", group: "dba", wantMsg: cpNoUsersScopeMsg},
		{name: "linked slack id, no email", slackID: "U-LINK-NOEMAIL", group: "dba", wantEmail: "noemail@corp.com"},
		{name: "no link, email matches ignoring case", slackID: "U-EMAIL", group: "dba", wantEmail: "email@corp.com"},
		{name: "no link, email matches an invited user", slackID: "U-PENDING", group: "dba", wantEmail: "pending@corp.com"},
		{name: "stranger", slackID: "U-STRANGER", group: "dba", wantMsg: cpOtherWorkspaceMsg},
		{name: "other team", slackID: "U-OTHERTEAM", group: "dba", wantMsg: cpOtherWorkspaceMsg},
		{name: "guest", slackID: "U-GUEST", group: "dba", wantMsg: cpGuestMsg},
		{name: "no email", slackID: "U-NOEMAIL", group: "dba", wantMsg: cpNotLinkedMsg},
		{name: "missing scope", slackID: "U-SCOPE", group: "dba", wantMsg: cpNotLinkedMsg},
		{name: "slack api error", slackID: "U-APIERR", group: "dba", wantMsg: cpNotVerifiedMsg},
		{name: "zero email matches", slackID: "U-NOBODY", group: "dba", wantMsg: "No Hoop user has the email nobody@corp.com."},
		{name: "two email matches", slackID: "U-DUP", group: "dba", wantMsg: "More than one Hoop user has the email dup@corp.com."},
		{name: "user outside the group", slackID: "U-NOGROUP", group: "dba", wantMsg: `You do not belong to group "dba".`},
		{name: "clicked another group", slackID: "U-OTHERGROUP", group: "sre", wantMsg: `You do not belong to group "sre".`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(f.ephemerals)
			ctx := p.resolveControlPlaneApprover(clickEvent(ss, tt.slackID, tt.group))
			if tt.wantEmail != "" {
				if ctx == nil {
					t.Fatalf("refused with %q, want approver %s", f.last(), tt.wantEmail)
				}
				if ctx.UserEmail != tt.wantEmail || ctx.SlackID != tt.slackID || ctx.OrgID != approverOrgID {
					t.Errorf("context = email %q slack %q org %q", ctx.UserEmail, ctx.SlackID, ctx.OrgID)
				}
				return
			}
			if ctx != nil {
				t.Fatalf("approved %s, want refusal %q", ctx.UserEmail, tt.wantMsg)
			}
			if len(f.ephemerals) == before || !strings.HasPrefix(f.last(), tt.wantMsg) {
				t.Errorf("ephemeral = %q, want prefix %q", f.last(), tt.wantMsg)
			}
		})
	}
}

// The gateway path is main's, word for word: the association link for an
// unknown Slack user, and the stored slack_id on the approver's context.
func TestGatewayApproverMatchesMain(t *testing.T) {
	startApproverDB(t)
	seedApprover(t, "linked@corp.com", "active", "U-LINK", "dba")
	f := &fakeSlack{users: map[string]map[string]any{}}
	ss := newFakeSlackService(t, f)
	p := &slackPlugin{apiURL: "http://localhost:8009"}

	if ctx := p.resolveHoopApprover(clickEvent(ss, "U-NONE", "dba")); ctx != nil {
		t.Fatalf("unlinked user approved as %s", ctx.UserEmail)
	}
	want := "You are not registered. Visit the link to associate your Slack user with Hoop.\n" +
		"http://localhost:8009/slack/user/new/U-NONE"
	if f.last() != want {
		t.Errorf("unlinked message = %q, want %q", f.last(), want)
	}

	if ctx := p.resolveHoopApprover(clickEvent(ss, "U-LINK", "sre")); ctx != nil {
		t.Fatalf("user outside the group approved")
	}
	if want := `You do not belong to group "sre".`; f.last() != want {
		t.Errorf("group message = %q, want %q", f.last(), want)
	}

	ctx := p.resolveHoopApprover(clickEvent(ss, "U-LINK", "dba"))
	if ctx == nil {
		t.Fatalf("linked user refused: %q", f.last())
	}
	if ctx.SlackID != "U-LINK" || ctx.UserEmail != "linked@corp.com" {
		t.Errorf("context = slack %q email %q", ctx.SlackID, ctx.UserEmail)
	}
}
