package directorysync

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
)

const syncOrgID = "00000000-0000-0000-0000-0000000000c1"

// fakeSlack is a workspace as users.list and usergroups.list report it.
type fakeSlack struct {
	users  []slackservice.DirectoryUser
	groups []slackservice.UserGroup
	err    error
}

func (f *fakeSlack) ListUsers(context.Context) ([]slackservice.DirectoryUser, error) {
	return f.users, f.err
}
func (f *fakeSlack) ListUserGroups(context.Context) ([]slackservice.UserGroup, error) {
	return f.groups, f.err
}

// use makes the import read f instead of the org's Slack app.
func (f *fakeSlack) use(t *testing.T) {
	t.Helper()
	orig := openSlack
	openSlack = func(string) (slackDirectory, error) { return f, nil }
	t.Cleanup(func() { openSlack = orig })
}

func (f *fakeSlack) user(id string) *slackservice.DirectoryUser {
	for i := range f.users {
		if f.users[i].ID == id {
			return &f.users[i]
		}
	}
	return nil
}

func (f *fakeSlack) group(id string) *slackservice.UserGroup {
	for i := range f.groups {
		if f.groups[i].ID == id {
			return &f.groups[i]
		}
	}
	return nil
}

func member(id, email string) slackservice.DirectoryUser {
	return slackservice.DirectoryUser{
		SlackUser: slackservice.SlackUser{ID: id, Email: email, IsEmailConfirmed: true},
		Name:      strings.Split(email, "@")[0],
	}
}

func slackWorkspace() *fakeSlack {
	admin := member("U-ADMIN", "root@corp.com")
	admin.IsAdmin = true
	bot := member("U-BOT", "bot@corp.com")
	bot.IsBot = true
	guest := member("U-GUEST", "guest@corp.com")
	guest.IsRestricted = true
	stranger := member("U-STRANGER", "stranger@other.com")
	stranger.IsStranger = true
	return &fakeSlack{
		users: []slackservice.DirectoryUser{
			admin, member("U-ANA", "Ana@Corp.com"), member("U-BOB", "bob@corp.com"), member("U-EVE", "eve@corp.com"),
			bot, guest, stranger,
		},
		groups: []slackservice.UserGroup{
			{ID: "S-DBA", Handle: "dba-leads", CreatedBy: "U-EVE", UpdatedBy: "U-ADMIN",
				Users: []string{"U-ADMIN", "U-ANA", "U-BOB", "U-BOT", "U-GUEST", "U-STRANGER"}},
			{ID: "S-SRE", Handle: "sre", CreatedBy: "U-EVE", Users: []string{"U-EVE"}},
			{ID: "S-ADMIN", Handle: "admin", CreatedBy: "U-ADMIN", Users: []string{"U-EVE"}},
			{ID: "S-SHARED", Handle: "partners", CreatedBy: "U-ADMIN", IsExternal: true, Users: []string{"U-ANA"}},
		},
	}
}

func TestSlackWorkspace(t *testing.T) {
	ctx := context.Background()
	f := slackWorkspace()
	f.use(t)

	groups, err := ListGroups(ctx, syncOrgID)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
		// dba-leads and admin were last edited by an admin; sre only ever
		// by a member.
		if want := g.Name != "sre"; g.AdminManaged != want {
			t.Errorf("%s admin_managed = %v, want %v", g.Name, g.AdminManaged, want)
		}
	}
	if !slices.Equal(names, []string{"admin", "dba-leads", "sre"}) {
		t.Errorf("groups = %v; want handles, without the external group", names)
	}

	w, err := readWorkspace(ctx, f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var ids []string
	for _, m := range w.members(w.groups["S-DBA"]) {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(ids, []string{"U-ADMIN", "U-ANA", "U-BOB"}) {
		t.Errorf("members = %v; want no bot, guest or stranger", ids)
	}

	if _, err := w.selected([]string{"S-DBA"}, false); err != nil {
		t.Errorf("admin-managed group refused: %v", err)
	}
	_, err = w.selected([]string{"S-DBA", "S-SRE"}, false)
	if err == nil || !strings.Contains(err.Error(), "@sre") || strings.Contains(err.Error(), "@dba-leads") {
		t.Errorf("member-managed group: err = %v; want @sre named", err)
	}
	if _, err := w.selected([]string{"S-SRE"}, true); err != nil {
		t.Errorf("allowed member-managed group refused: %v", err)
	}
	if _, err := w.selected([]string{"S-SHARED"}, true); err == nil {
		t.Errorf("an external group was accepted")
	}

	if _, err := readWorkspace(ctx, &fakeSlack{err: errors.New("slack down")}); err == nil {
		t.Errorf("want the Slack error")
	}
	orig := openSlack
	openSlack = func(string) (slackDirectory, error) { return nil, ErrSlackNotConfigured }
	defer func() { openSlack = orig }()
	if _, err := ListGroups(ctx, syncOrgID); !errors.Is(err, ErrSlackNotConfigured) {
		t.Errorf("err = %v; want ErrSlackNotConfigured", err)
	}
}

func startSyncDB(t *testing.T) {
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
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'sync-test')`, syncOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

type userRow struct {
	Status  string
	Subject string
	SlackID *string
}

func userByEmail(t *testing.T, email string) userRow {
	t.Helper()
	var row userRow
	if err := models.DB.Raw(`SELECT status, subject, slack_id FROM private.users WHERE org_id = ? AND email = ?`,
		syncOrgID, email).Scan(&row).Error; err != nil {
		t.Fatalf("load %s: %v", email, err)
	}
	return row
}

func groupsOf(t *testing.T, email string) []string {
	t.Helper()
	var names []string
	if err := models.DB.Raw(`
		SELECT ug.name FROM private.user_groups ug JOIN private.users u ON u.id = ug.user_id
		WHERE u.org_id = ? AND u.email = ? ORDER BY ug.name`, syncOrgID, email).Scan(&names).Error; err != nil {
		t.Fatalf("list groups: %v", err)
	}
	return names
}

func syncConfig(groupIDs ...string) *models.DirectorySyncConfig {
	return &models.DirectorySyncConfig{OrgID: syncOrgID, GroupIDs: pq.StringArray(groupIDs), IntervalMinutes: 15}
}

// importWith stores cfg as the org's import and runs it against f.
func importWith(t *testing.T, f *fakeSlack, cfg *models.DirectorySyncConfig) (*runDiff, error) {
	t.Helper()
	if err := models.UpsertDirectorySyncConfig(models.DB, cfg); err != nil {
		t.Fatalf("store config: %v", err)
	}
	return reconcile(context.Background(), models.DB, syncOrgID, f, cfg)
}

func TestReconcileSlack(t *testing.T) {
	startSyncDB(t)

	// The org's admin, who logged in before the sync existed and is also a
	// Slack workspace admin in dba-leads.
	rootID := uuid.NewString()
	if err := models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status)
		VALUES (?, ?, 'idp|root', 'root@corp.com', 'Root', 'active')`, rootID, syncOrgID).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if err := models.InsertUserGroups([]models.UserGroup{{OrgID: syncOrgID, UserID: rootID, Name: types.GroupAdmin}}); err != nil {
		t.Fatalf("seed admin group: %v", err)
	}

	f := slackWorkspace()
	diff, err := importWith(t, f, syncConfig("S-DBA"))
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !slices.Equal(diff.UsersCreated, []string{"ana@corp.com", "bob@corp.com"}) {
		t.Errorf("created = %v; root existed already", diff.UsersCreated)
	}
	for email, slackID := range map[string]string{"ana@corp.com": "U-ANA", "bob@corp.com": "U-BOB", "root@corp.com": "U-ADMIN"} {
		if got := userByEmail(t, email); got.Status != "active" || got.SlackID == nil || *got.SlackID != slackID {
			t.Errorf("%s = %+v; want active with slack id %s", email, got, slackID)
		}
		if got := groupsOf(t, email); !slices.Contains(got, "dba-leads") {
			t.Errorf("%s groups = %v; want dba-leads", email, got)
		}
	}
	if got := groupsOf(t, "root@corp.com"); !slices.Equal(got, []string{types.GroupAdmin, "dba-leads"}) {
		t.Errorf("root groups = %v; the admin row must stay", got)
	}
	if got := userByEmail(t, "ana@corp.com"); got.Subject != "slack|U-ANA" {
		t.Errorf("ana subject = %q; want the placeholder the first login replaces", got.Subject)
	}
	if got := userByEmail(t, "root@corp.com"); got.Subject != "idp|root" {
		t.Errorf("root subject = %q; adopting a user must keep their login", got.Subject)
	}
	managed, err := models.GroupsManagedByProvisioning(models.DB, syncOrgID)
	if err != nil || !managed {
		t.Errorf("after a slack run: managed=%v err=%v; want true", managed, err)
	}

	t.Run("a member-managed group refuses the whole run", func(t *testing.T) {
		_, err := importWith(t, f, syncConfig("S-DBA", "S-SRE"))
		if err == nil || !strings.Contains(err.Error(), "@sre") {
			t.Fatalf("err = %v; want the governance refusal", err)
		}
		if got := groupsOf(t, "eve@corp.com"); len(got) != 0 {
			t.Errorf("eve groups = %v; nothing may be written", got)
		}
		cfg := syncConfig("S-DBA", "S-SRE")
		cfg.AllowMemberManagedGroups = true
		if _, err := importWith(t, f, cfg); err != nil {
			t.Fatalf("allowed: %v", err)
		}
		if got := groupsOf(t, "eve@corp.com"); !slices.Equal(got, []string{"sre"}) {
			t.Errorf("eve groups = %v; want sre", got)
		}
	})

	t.Run("a group named admin is refused and admins keep their row", func(t *testing.T) {
		_, err := importWith(t, f, syncConfig("S-DBA", "S-ADMIN"))
		if !errors.Is(err, ErrReservedGroupName) {
			t.Fatalf("err = %v; want ErrReservedGroupName", err)
		}
		if got := groupsOf(t, "eve@corp.com"); slices.Contains(got, types.GroupAdmin) {
			t.Errorf("eve became admin: %v", got)
		}
		if got := groupsOf(t, "root@corp.com"); !slices.Contains(got, types.GroupAdmin) {
			t.Errorf("root lost admin: %v", got)
		}
	})

	t.Run("a renamed handle keeps the hoop group name", func(t *testing.T) {
		f.group("S-DBA").Handle = "dba"
		cfg := syncConfig("S-DBA", "S-SRE")
		cfg.AllowMemberManagedGroups = true
		if _, err := importWith(t, f, cfg); err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := groupsOf(t, "ana@corp.com"); !slices.Equal(got, []string{"dba-leads"}) {
			t.Errorf("ana groups = %v; a rule naming dba-leads must keep working", got)
		}
		f.group("S-DBA").Handle = "dba-leads"
	})

	t.Run("a user found twice by email refuses the run", func(t *testing.T) {
		for _, subject := range []string{"idp|carl-1", "idp|carl-2"} {
			if err := models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status)
				VALUES (?, ?, ?, 'carl@corp.com', 'Carl', 'active')`, uuid.NewString(), syncOrgID, subject).Error; err != nil {
				t.Fatalf("seed carl: %v", err)
			}
		}
		f.users = append(f.users, member("U-CARL", "carl@corp.com"))
		g := f.group("S-DBA")
		g.Users = append(g.Users, "U-CARL")
		t.Cleanup(func() {
			g.Users = slices.DeleteFunc(g.Users, func(id string) bool { return id == "U-CARL" })
		})
		_, err := importWith(t, f, syncConfig("S-DBA"))
		if !errors.Is(err, ErrAmbiguousUser) {
			t.Fatalf("err = %v; want ErrAmbiguousUser", err)
		}
	})

	t.Run("a member who leaves keeps the account and loses the group", func(t *testing.T) {
		g := f.group("S-DBA")
		g.Users = slices.DeleteFunc(g.Users, func(id string) bool { return id == "U-BOB" })
		diff, err := importWith(t, f, syncConfig("S-DBA"))
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := userByEmail(t, "bob@corp.com"); got.Status != "active" {
			t.Errorf("bob = %+v; leaving a group is not leaving the company", got)
		}
		if got := groupsOf(t, "bob@corp.com"); len(got) != 0 {
			t.Errorf("bob groups = %v; want none", got)
		}
		if got := diff.MembershipsRemoved["dba-leads"]; !slices.Contains(got, "bob@corp.com") {
			t.Errorf("removed = %v; want bob in the diff", diff.MembershipsRemoved)
		}
		if !slices.Contains(diff.GroupsRemoved, "sre") {
			t.Errorf("groups removed = %v; sre is no longer selected", diff.GroupsRemoved)
		}
	})

	t.Run("a user deleted in Slack is deactivated, an admin is not", func(t *testing.T) {
		f.user("U-ANA").Deleted = true
		f.user("U-ADMIN").Deleted = true
		diff, err := importWith(t, f, syncConfig("S-DBA"))
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := userByEmail(t, "ana@corp.com"); got.Status != "inactive" {
			t.Errorf("ana = %+v; want inactive", got)
		}
		if !slices.Equal(diff.UsersDeactivated, []string{"ana@corp.com"}) {
			t.Errorf("deactivated = %v; want only ana", diff.UsersDeactivated)
		}
		if got := userByEmail(t, "root@corp.com"); got.Status != "active" {
			t.Errorf("root = %+v; an admin is never deactivated by a sync", got)
		}
		if got := groupsOf(t, "root@corp.com"); !slices.Equal(got, []string{types.GroupAdmin}) {
			t.Errorf("root groups = %v; want only admin", got)
		}
	})

	t.Run("removing the import keeps users and groups and stops managing them", func(t *testing.T) {
		g := f.group("S-DBA")
		g.Users = append(g.Users, "U-BOB")
		if _, err := importWith(t, f, syncConfig("S-DBA")); err != nil {
			t.Fatalf("run: %v", err)
		}
		if err := Remove(models.DB, syncOrgID); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if _, err := models.GetDirectorySyncConfig(models.DB, syncOrgID); err == nil {
			t.Errorf("the import config survived")
		}
		if managed, err := models.GroupsManagedByProvisioning(models.DB, syncOrgID); err != nil || managed {
			t.Errorf("after remove: managed=%v err=%v; want false", managed, err)
		}
		if got := groupsOf(t, "bob@corp.com"); !slices.Equal(got, []string{"dba-leads"}) {
			t.Errorf("bob groups = %v; removing the import keeps them", got)
		}

		// A run that read Slack before the removal writes nothing.
		_, err := reconcile(context.Background(), models.DB, syncOrgID, f, syncConfig("S-DBA"))
		if !errors.Is(err, errImportRemoved) {
			t.Fatalf("err = %v; want errImportRemoved", err)
		}
		if managed, _ := models.GroupsManagedByProvisioning(models.DB, syncOrgID); managed {
			t.Errorf("a run after remove made the groups managed again")
		}
	})
}

func TestRunRecordsResultAndAudit(t *testing.T) {
	startSyncDB(t)
	slackWorkspace().use(t)

	if err := models.UpsertDirectorySyncConfig(models.DB, syncConfig("S-DBA")); err != nil {
		t.Fatalf("store config: %v", err)
	}
	actor := Actor{Subject: "idp|root", Email: "root@corp.com", Name: "Root"}
	if err := Run(context.Background(), models.DB, syncOrgID, actor); err != nil {
		t.Fatalf("run: %v", err)
	}
	cfg, err := models.GetDirectorySyncConfig(models.DB, syncOrgID)
	if err != nil || cfg.LastRunAt == nil || cfg.LastError != nil {
		t.Fatalf("config = %+v err %v; want a successful last run", cfg, err)
	}

	var rows []models.SecurityAuditLog
	if err := models.DB.Where("org_id = ? AND resource_type = 'provisioning'", syncOrgID).Find(&rows).Error; err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(rows) != 1 || !rows[0].Outcome || rows[0].Action != "sync" || rows[0].ActorEmail != "root@corp.com" {
		t.Fatalf("audit rows = %+v; want one successful sync by root", rows)
	}
	created, _ := rows[0].RequestPayloadRedacted["users_created"].([]any)
	if len(created) != 3 {
		t.Errorf("audit payload users_created = %v; want the three members", rows[0].RequestPayloadRedacted)
	}

	// A refused run records the error on the config and in the audit log.
	if err := models.UpsertDirectorySyncConfig(models.DB, syncConfig("S-SRE")); err != nil {
		t.Fatalf("store config: %v", err)
	}
	if err := Run(context.Background(), models.DB, syncOrgID, SystemActor); err == nil {
		t.Fatalf("want the governance refusal")
	}
	cfg, _ = models.GetDirectorySyncConfig(models.DB, syncOrgID)
	if cfg.LastError == nil || !strings.Contains(*cfg.LastError, "@sre") {
		t.Errorf("last_error = %v; want the refusal", cfg.LastError)
	}
	var failed int64
	models.DB.Model(&models.SecurityAuditLog{}).
		Where("org_id = ? AND resource_type = 'provisioning' AND outcome = false AND actor_subject = 'system'", syncOrgID).
		Count(&failed)
	if failed != 1 {
		t.Errorf("failed audit rows = %d; want 1", failed)
	}
}

func TestDue(t *testing.T) {
	now := time.Now().UTC()
	recent := now.Add(-5 * time.Minute)
	old := now.Add(-20 * time.Minute)
	for _, tt := range []struct {
		name string
		cfg  models.DirectorySyncConfig
		want bool
	}{
		{"no groups", models.DirectorySyncConfig{IntervalMinutes: 15}, false},
		{"never ran", models.DirectorySyncConfig{GroupIDs: pq.StringArray{"S1"}, IntervalMinutes: 15}, true},
		{"ran recently", models.DirectorySyncConfig{GroupIDs: pq.StringArray{"S1"}, IntervalMinutes: 15, LastRunAt: &recent}, false},
		{"interval passed", models.DirectorySyncConfig{GroupIDs: pq.StringArray{"S1"}, IntervalMinutes: 15, LastRunAt: &old}, true},
	} {
		if got := due(tt.cfg, now); got != tt.want {
			t.Errorf("%s: due = %v, want %v", tt.name, got, tt.want)
		}
	}
}
