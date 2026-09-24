// Package directorysync pulls users and groups from identity providers that do
// not push SCIM (Google Workspace, Auth0, Cognito) into the control plane
// (ADR-0019). Every provider is read through the same two calls, and every run
// writes through gateway/services provisioning, the same path SCIM uses.
package directorysync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"gorm.io/gorm"
)

// Group is a group of the identity provider. Name is what hoop stores as the
// group name, so it must be what the provider's login claim calls it too.
type Group struct {
	ID   string
	Name string
}

// User is a member of a group, as the identity provider reports it.
type User struct {
	ExternalID string
	Email      string
	Name       string
	Active     bool
}

// Provider reads one identity provider's directory.
type Provider interface {
	ListGroups(ctx context.Context) ([]Group, error)
	ListMembers(ctx context.Context, groupID string) ([]User, error)
}

// ErrSyncRunning refuses a run while another one for the same org is writing.
var ErrSyncRunning = errors.New("a directory sync is already running for this organization")

// runTimeout bounds one run: a provider that stops answering must not hold
// the org's lock forever.
const runTimeout = 10 * time.Minute

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

// Run syncs one organization now and records the outcome on its config.
func Run(ctx context.Context, db *gorm.DB, orgID string) error {
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
	runErr := func() error {
		provider, err := NewProvider(ctx, cfg.Provider, cfg.Settings)
		if err != nil {
			return err
		}
		return reconcile(ctx, db, orgID, cfg.Provider, provider, cfg.GroupIDs)
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
		log.With("org", orgID, "provider", cfg.Provider).Infof("directory sync finished in %v",
			time.Since(started).Round(time.Millisecond))
	}
	if err := models.SetDirectorySyncResult(db, orgID, started, errMsg); err != nil {
		log.With("org", orgID).Warnf("failed recording the directory sync result, reason=%v", err)
	}
	return runErr
}

type fetchedGroup struct {
	group   Group
	members []User
}

// reconcile makes hoop match the provider for the selected groups.
//
// Everything is read before anything is written, and the writes are one
// transaction: a provider error mid-run leaves hoop exactly as the last good
// run left it, instead of deactivating everyone who was not read yet.
func reconcile(ctx context.Context, db *gorm.DB, orgID, source string, p Provider, groupIDs []string) error {
	all, err := p.ListGroups(ctx)
	if err != nil {
		return fmt.Errorf("failed listing groups: %w", err)
	}
	byID := make(map[string]Group, len(all))
	for _, g := range all {
		byID[g.ID] = g
	}
	var fetched []fetchedGroup
	for _, id := range groupIDs {
		g, ok := byID[id]
		if !ok {
			return fmt.Errorf("group %s no longer exists in %s; remove it from the sync", id, source)
		}
		members, err := p.ListMembers(ctx, id)
		if err != nil {
			return fmt.Errorf("failed listing members of group %s: %w", g.Name, err)
		}
		fetched = append(fetched, fetchedGroup{group: g, members: members})
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var locked bool
		if err := tx.Raw(`SELECT pg_try_advisory_xact_lock(hashtext(?))`, "directory-sync:"+orgID).
			Scan(&locked).Error; err != nil {
			return err
		}
		if !locked {
			return ErrSyncRunning
		}
		return write(tx, orgID, source, fetched)
	})
}

func write(tx *gorm.DB, orgID, source string, fetched []fetchedGroup) error {
	userIDs := map[string]string{} // external id -> hoop user id
	active := map[string]bool{}
	for _, fg := range fetched {
		for _, m := range fg.members {
			key := memberKey(m)
			if key == "" {
				continue
			}
			if _, seen := userIDs[key]; seen {
				continue
			}
			id, err := services.UpsertProvisionedUser(tx, orgID, source, "", services.ProvisionedUser{
				ExternalID: key,
				UserName:   m.Email,
				Email:      m.Email,
				Name:       m.Name,
				Active:     m.Active,
			})
			if errors.Is(err, services.ErrProvisionedUserEmailRequired) {
				log.With("org", orgID, "provider", source).Warnf("skipping member %s: no email", key)
				continue
			}
			if err != nil {
				return err
			}
			userIDs[key] = id
			active[key] = m.Active
		}
	}

	selected := map[string]bool{}
	for _, fg := range fetched {
		g, err := services.EnsureProvisionedGroup(tx, orgID, source, fg.group.Name, fg.group.ID)
		if err != nil {
			return err
		}
		selected[g.ID] = true
		var members []string
		for _, m := range fg.members {
			if id, ok := userIDs[memberKey(m)]; ok && active[memberKey(m)] {
				members = append(members, id)
			}
		}
		if err := services.SetGroupMembers(tx, orgID, g.DisplayName, members); err != nil {
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
		if err := leaveScope(tx, orgID, link.UserID); err != nil {
			return err
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
	}
	return nil
}

// leaveScope handles a user who is in none of the synced groups any more.
// They are deactivated, unless they are an admin: an admin whom the sync once
// matched by email must not be locked out of the control plane because an
// admin narrowed the group selection. They only lose the synced groups.
func leaveScope(tx *gorm.DB, orgID, userID string) error {
	var isAdmin bool
	if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM private.user_groups WHERE user_id::TEXT = ? AND name = ?)`,
		userID, types.GroupAdmin).Scan(&isAdmin).Error; err != nil {
		return err
	}
	if !isAdmin {
		return services.DeactivateProvisionedUser(tx, orgID, userID)
	}
	return tx.Exec(`
		DELETE FROM private.user_groups
		WHERE org_id = @org AND user_id::TEXT = @user
		AND name IN (SELECT display_name FROM private.directory_groups WHERE org_id = @org)`,
		map[string]any{"org": orgID, "user": userID}).Error
}

// memberKey identifies a member across runs: the provider's id, else the
// email.
func memberKey(u User) string {
	if u.ExternalID != "" {
		return u.ExternalID
	}
	return strings.ToLower(strings.TrimSpace(u.Email))
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
		if err := Run(ctx, db, cfg.OrgID); err != nil && !errors.Is(err, ErrSyncRunning) {
			// Run already logged and recorded it on the config.
			continue
		}
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

// decodeSettings reads a provider's settings strictly: a field hoop does not
// know is a typo that would otherwise sync with a default in its place.
func decodeSettings(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid settings: %w", err)
	}
	return nil
}
