// Package directorysync pulls users and groups into the control plane from a
// directory that does not push them (ADR-0019). Today that directory is Slack:
// the members of the user groups an admin picks become hoop users in hoop
// groups of the same handle. Every run writes through gateway/services
// provisioning, the same path SCIM and the file import use.
package directorysync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"gorm.io/gorm"
)

// Group is a group of the directory. Name is what hoop stores as the group
// name, and what a rule's reviewers_groups must say.
type Group struct {
	ID   string
	Name string
}

// User is an active member of a group. ExternalID is the directory's id for
// the person.
type User struct {
	ExternalID string
	Email      string
	Name       string
}

// Provider reads one directory. ListMembers returns active members only; a
// member the directory deactivated is reported by DeletedUsers instead, when
// the provider implements deletionReporter.
type Provider interface {
	ListGroups(ctx context.Context) ([]Group, error)
	ListMembers(ctx context.Context, groupID string) ([]User, error)
}

// groupGuard is implemented by a provider that can tell whether the groups
// picked may be trusted as reviewers, and refuses the run when not.
type groupGuard interface {
	CheckGroups(ctx context.Context, groupIDs []string, allowMemberManaged bool) error
}

// deletionReporter is implemented by a provider that knows which people it
// deactivated, by external id. Only they are deactivated in hoop.
type deletionReporter interface {
	DeletedUsers(ctx context.Context) (map[string]bool, error)
}

// slackLinker is implemented by a provider whose external ids are Slack user
// ids, so a run also writes users.slack_id.
type slackLinker interface {
	LinksSlackID() bool
}

// Actor is who a run is recorded against in the audit log.
type Actor struct {
	Subject string
	Email   string
	Name    string
}

// SystemActor is the actor of a run the scheduler started.
var SystemActor = Actor{Subject: "system", Name: "directory sync"}

// ErrSyncRunning refuses a run while another one for the same org is writing.
var ErrSyncRunning = errors.New("a directory sync is already running for this organization")

// runTimeout bounds one run: a directory that stops answering must not hold
// the org's lock forever.
const runTimeout = 10 * time.Minute

// auditListCap bounds each list in a run's audit entry.
const auditListCap = 500

var (
	runningMu sync.Mutex
	running   = map[string]bool{}
)

func claim(orgID string) bool {
	runningMu.Lock()
	defer runningMu.Unlock()
	if running[orgID] {
		return false
	}
	running[orgID] = true
	return true
}

func release(orgID string) {
	runningMu.Lock()
	defer runningMu.Unlock()
	delete(running, orgID)
}

// newProvider builds the provider of a sync config. Tests replace it.
var newProvider = func(orgID string, cfg *models.DirectorySyncConfig) (Provider, error) {
	if cfg.Provider != models.ProvisioningSourceSlack {
		return nil, fmt.Errorf("unknown directory sync provider %q", cfg.Provider)
	}
	return NewSlackProvider(orgID)
}

// Run syncs one organization now, records the outcome on its config and
// writes one audit entry with what changed.
func Run(ctx context.Context, db *gorm.DB, orgID string, actor Actor) error {
	if !claim(orgID) {
		return ErrSyncRunning
	}
	defer release(orgID)

	cfg, err := models.GetDirectorySyncConfig(db, orgID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	started := time.Now().UTC()
	var diff *runDiff
	runErr := func() error {
		provider, err := newProvider(orgID, cfg)
		if err != nil {
			return err
		}
		diff, err = reconcile(ctx, db, orgID, cfg.Provider, provider, cfg)
		return err
	}()
	if errors.Is(runErr, ErrSyncRunning) {
		return runErr
	}

	var errMsg *string
	if runErr != nil {
		msg := runErr.Error()
		errMsg = &msg
		log.With("org", orgID, "provider", cfg.Provider).Warnf("directory sync failed, reason=%v", runErr)
	} else {
		log.With("org", orgID, "provider", cfg.Provider).Infof("directory sync finished in %v, %s",
			time.Since(started).Round(time.Millisecond), diff.summary())
	}
	if err := models.SetDirectorySyncResult(db, orgID, started, errMsg); err != nil {
		log.With("org", orgID).Warnf("failed recording the directory sync result, reason=%v", err)
	}
	recordRun(orgID, cfg.Provider, actor, diff, runErr)
	return runErr
}

type fetchedGroup struct {
	group   Group
	members []User
}

// reconcile makes hoop match the directory for the selected groups.
//
// Everything is read before anything is written, and the writes are one
// transaction: a directory error mid-run leaves hoop exactly as the last good
// run left it.
func reconcile(ctx context.Context, db *gorm.DB, orgID, source string, p Provider, cfg *models.DirectorySyncConfig) (*runDiff, error) {
	all, err := p.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed listing groups: %w", err)
	}
	byID := make(map[string]Group, len(all))
	for _, g := range all {
		byID[g.ID] = g
	}
	for _, id := range cfg.GroupIDs {
		g, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("group %s no longer exists in %s; remove it from the sync", id, source)
		}
		if services.IsReservedGroupName(g.Name) {
			return nil, fmt.Errorf("group %s: %w", g.Name, services.ErrReservedGroupName)
		}
	}
	if guard, ok := p.(groupGuard); ok {
		if err := guard.CheckGroups(ctx, cfg.GroupIDs, cfg.AllowMemberManagedGroups); err != nil {
			return nil, err
		}
	}
	var fetched []fetchedGroup
	for _, id := range cfg.GroupIDs {
		members, err := p.ListMembers(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed listing members of group %s: %w", byID[id].Name, err)
		}
		fetched = append(fetched, fetchedGroup{group: byID[id], members: members})
	}
	deleted := map[string]bool{}
	if r, ok := p.(deletionReporter); ok {
		if deleted, err = r.DeletedUsers(ctx); err != nil {
			return nil, fmt.Errorf("failed listing deactivated users: %w", err)
		}
	}
	linker, _ := p.(slackLinker)
	linkSlack := linker != nil && linker.LinksSlackID()

	diff := newRunDiff()
	err = db.Transaction(func(tx *gorm.DB) error {
		var locked bool
		if err := tx.Raw(`SELECT pg_try_advisory_xact_lock(hashtext(?))`, "directory-sync:"+orgID).
			Scan(&locked).Error; err != nil {
			return err
		}
		if !locked {
			return ErrSyncRunning
		}
		return write(tx, orgID, source, fetched, deleted, linkSlack, diff)
	})
	if err != nil {
		return nil, err
	}
	return diff, nil
}

func write(tx *gorm.DB, orgID, source string, fetched []fetchedGroup, deleted map[string]bool, linkSlack bool, diff *runDiff) error {
	userIDs := map[string]string{} // member key -> hoop user id
	for _, fg := range fetched {
		for _, m := range fg.members {
			key := memberKey(m)
			if key == "" {
				continue
			}
			if _, seen := userIDs[key]; seen {
				continue
			}
			pu := services.ProvisionedUser{
				ExternalID: key,
				UserName:   m.Email,
				Email:      m.Email,
				Name:       m.Name,
				Active:     true,
			}
			if linkSlack {
				pu.SlackID = m.ExternalID
			}
			res, err := services.UpsertProvisionedUserResult(tx, orgID, source, "", pu)
			if errors.Is(err, services.ErrProvisionedUserEmailRequired) {
				log.With("org", orgID, "provider", source).Warnf("skipping member %s: no email", key)
				continue
			}
			if err != nil {
				return fmt.Errorf("member %s: %w", m.Email, err)
			}
			userIDs[key] = res.UserID
			if res.Created {
				diff.UsersCreated = append(diff.UsersCreated, strings.ToLower(m.Email))
			}
		}
	}

	selected := map[string]bool{}
	for _, fg := range fetched {
		g, err := services.EnsureProvisionedGroup(tx, orgID, source, fg.group.Name, fg.group.ID)
		if err != nil {
			return fmt.Errorf("group %s: %w", fg.group.Name, err)
		}
		selected[g.ID] = true
		before, err := models.ListGroupMemberIDs(tx, orgID, g.DisplayName)
		if err != nil {
			return err
		}
		var members []string
		for _, m := range fg.members {
			if id, ok := userIDs[memberKey(m)]; ok {
				members = append(members, id)
			}
		}
		if err := services.SetGroupMembers(tx, orgID, g.DisplayName, members); err != nil {
			return err
		}
		if err := diff.recordMembership(tx, g.DisplayName, before, members); err != nil {
			return err
		}
	}

	seenUsers := map[string]bool{}
	for _, id := range userIDs {
		seenUsers[id] = true
	}
	links, err := models.ListDirectoryUsers(tx, orgID, source)
	if err != nil {
		return err
	}
	for _, link := range links {
		if seenUsers[link.UserID] {
			continue
		}
		gone := link.ExternalID != nil && deleted[*link.ExternalID]
		deactivated, err := leaveScope(tx, orgID, link.UserID, gone)
		if err != nil {
			return err
		}
		if deactivated {
			diff.UsersDeactivated = append(diff.UsersDeactivated, strings.ToLower(link.UserName))
		}
	}

	groups, err := models.ListDirectoryGroups(tx, orgID, source)
	if err != nil {
		return err
	}
	for i := range groups {
		if selected[groups[i].ID] {
			continue
		}
		if err := services.DeleteProvisionedGroup(tx, orgID, &groups[i]); err != nil {
			return err
		}
		diff.GroupsRemoved = append(diff.GroupsRemoved, groups[i].DisplayName)
	}
	return nil
}

// leaveScope handles a user the run did not see in any selected group. They
// keep their account and lose only the groups the sync owns: leaving a user
// group in Slack is not leaving the company. They are deactivated only when
// the directory says so (deleted), and never when they are an admin, so a
// sync cannot lock the org out of the control plane.
func leaveScope(tx *gorm.DB, orgID, userID string, deletedInSource bool) (deactivated bool, err error) {
	if deletedInSource {
		admin, err := isAdmin(tx, orgID, userID)
		if err != nil {
			return false, err
		}
		if !admin {
			return true, services.DeactivateProvisionedUser(tx, orgID, userID)
		}
	}
	return false, tx.Exec(`
		DELETE FROM private.user_groups
		WHERE org_id = @org AND user_id::TEXT = @user
		AND name IN (SELECT display_name FROM private.directory_groups WHERE org_id = @org)`,
		map[string]any{"org": orgID, "user": userID}).Error
}

func isAdmin(tx *gorm.DB, orgID, userID string) (bool, error) {
	var admin bool
	err := tx.Raw(`SELECT EXISTS (
		SELECT 1 FROM private.user_groups WHERE org_id = ? AND user_id::TEXT = ? AND name = ?)`,
		orgID, userID, types.GroupAdmin).Scan(&admin).Error
	return admin, err
}

// memberKey identifies a member across runs: the directory's id, else the
// email.
func memberKey(u User) string {
	if u.ExternalID != "" {
		return u.ExternalID
	}
	return strings.ToLower(strings.TrimSpace(u.Email))
}

// runDiff is what a run changed, for its audit entry.
type runDiff struct {
	UsersCreated       []string            `json:"users_created"`
	UsersDeactivated   []string            `json:"users_deactivated"`
	MembershipsAdded   map[string][]string `json:"memberships_added"`
	MembershipsRemoved map[string][]string `json:"memberships_removed"`
	GroupsRemoved      []string            `json:"groups_removed"`
}

func newRunDiff() *runDiff {
	return &runDiff{MembershipsAdded: map[string][]string{}, MembershipsRemoved: map[string][]string{}}
}

func (d *runDiff) recordMembership(tx *gorm.DB, group string, before, after []string) error {
	var added, removed []string
	for _, id := range after {
		if !slices.Contains(before, id) {
			added = append(added, id)
		}
	}
	for _, id := range before {
		if !slices.Contains(after, id) {
			removed = append(removed, id)
		}
	}
	if len(added)+len(removed) == 0 {
		return nil
	}
	emails, err := emailsOf(tx, append(append([]string{}, added...), removed...))
	if err != nil {
		return err
	}
	for _, id := range added {
		d.MembershipsAdded[group] = append(d.MembershipsAdded[group], emails[id])
	}
	for _, id := range removed {
		d.MembershipsRemoved[group] = append(d.MembershipsRemoved[group], emails[id])
	}
	return nil
}

func emailsOf(tx *gorm.DB, ids []string) (map[string]string, error) {
	var rows []struct {
		ID    string
		Email string
	}
	if err := tx.Raw(`SELECT id::TEXT AS id, email FROM private.users WHERE id::TEXT IN ?`, ids).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Email
	}
	return out, nil
}

func (d *runDiff) summary() string {
	if d == nil {
		return "no changes"
	}
	var added, removed int
	for _, v := range d.MembershipsAdded {
		added += len(v)
	}
	for _, v := range d.MembershipsRemoved {
		removed += len(v)
	}
	return fmt.Sprintf("created=%d deactivated=%d memberships-added=%d memberships-removed=%d groups-removed=%d",
		len(d.UsersCreated), len(d.UsersDeactivated), added, removed, len(d.GroupsRemoved))
}

func (d *runDiff) payload(source string) map[string]any {
	capList := func(v []string) []string {
		if len(v) > auditListCap {
			return v[:auditListCap]
		}
		return v
	}
	capMap := func(m map[string][]string) map[string][]string {
		out := make(map[string][]string, len(m))
		for k, v := range m {
			out[k] = capList(v)
		}
		return out
	}
	return map[string]any{
		"source":              source,
		"users_created":       capList(d.UsersCreated),
		"users_deactivated":   capList(d.UsersDeactivated),
		"memberships_added":   capMap(d.MembershipsAdded),
		"memberships_removed": capMap(d.MembershipsRemoved),
		"groups_removed":      capList(d.GroupsRemoved),
	}
}

// recordRun writes one security audit entry for the run: what changed on
// success, why it failed otherwise.
func recordRun(orgID, source string, actor Actor, diff *runDiff, runErr error) {
	row := &models.SecurityAuditLog{
		OrgID:        orgID,
		ActorSubject: actor.Subject,
		ActorEmail:   actor.Email,
		ActorName:    actor.Name,
		ResourceType: string(audit.ResourceProvisioning),
		Action:       string(audit.ActionSync),
		Outcome:      runErr == nil,
	}
	if runErr != nil {
		row.ErrorMessage = runErr.Error()
		row.RequestPayloadRedacted = map[string]any{"source": source}
	} else {
		row.RequestPayloadRedacted = diff.payload(source)
	}
	if err := models.CreateSecurityAuditLog(row); err != nil {
		log.With("org", orgID).Warnf("failed writing the directory sync audit entry, reason=%v", err)
	}
}

// Start runs every organization's sync on its interval until ctx is done. The
// control plane starts it; the gateway never does.
func Start(ctx context.Context, db *gorm.DB) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runDue(ctx, db, time.Now().UTC())
		}
	}
}

func runDue(ctx context.Context, db *gorm.DB, now time.Time) {
	configs, err := models.ListDirectorySyncConfigs(db)
	if err != nil {
		log.Warnf("failed listing directory syncs, reason=%v", err)
		return
	}
	for _, cfg := range configs {
		if !due(cfg, now) {
			continue
		}
		// Run logs and records every failure on the config.
		_ = Run(ctx, db, cfg.OrgID, SystemActor)
	}
}

func due(cfg models.DirectorySyncConfig, now time.Time) bool {
	if len(cfg.GroupIDs) == 0 {
		return false
	}
	if cfg.LastRunAt == nil {
		return true
	}
	return now.Sub(*cfg.LastRunAt) >= time.Duration(cfg.IntervalMinutes)*time.Minute
}
