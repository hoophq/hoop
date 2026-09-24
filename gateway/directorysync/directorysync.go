// Package directorysync imports the control plane's reviewers from Slack
// (ADR-0019): the members of the user groups an admin picks become hoop users
// in hoop groups named after the group handle. A Slack click then finds them
// by Slack ID, and nobody has to log in. The gateway never runs it.
package directorysync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// Actor is who a run is recorded against in the audit log.
type Actor struct {
	Subject string
	Email   string
	Name    string
}

// SystemActor is the actor of a run the scheduler started.
var SystemActor = Actor{Subject: "system", Name: "slack import"}

// ErrSyncRunning refuses a run while another one for the same org is writing.
var ErrSyncRunning = errors.New("a Slack import is already running for this organization")

// runTimeout bounds one run: a Slack API that stops answering must not hold
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

// Run imports one organization now, records the outcome on its config and
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
		api, err := openSlack(orgID)
		if err != nil {
			return err
		}
		diff, err = reconcile(ctx, db, orgID, api, cfg)
		return err
	}()
	if errors.Is(runErr, ErrSyncRunning) {
		return runErr
	}

	var errMsg *string
	if runErr != nil {
		msg := runErr.Error()
		errMsg = &msg
		log.With("org", orgID).Warnf("slack import failed, reason=%v", runErr)
	} else {
		log.With("org", orgID).Infof("slack import finished in %v, %s",
			time.Since(started).Round(time.Millisecond), diff.summary())
	}
	if err := models.SetDirectorySyncResult(db, orgID, started, errMsg); err != nil {
		log.With("org", orgID).Warnf("failed recording the slack import result, reason=%v", err)
	}
	recordRun(orgID, actor, diff, runErr)
	return runErr
}

// reconcile makes hoop match Slack for the selected groups.
//
// Slack is read before anything is written, and the writes are one
// transaction: a Slack error mid-run leaves hoop exactly as the last good run
// left it.
func reconcile(ctx context.Context, db *gorm.DB, orgID string, api slackDirectory, cfg *models.DirectorySyncConfig) (*runDiff, error) {
	w, err := readWorkspace(ctx, api)
	if err != nil {
		return nil, fmt.Errorf("failed reading slack: %w", err)
	}
	selected, err := w.selected(cfg.GroupIDs, cfg.AllowMemberManagedGroups)
	if err != nil {
		return nil, err
	}
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
		return write(tx, orgID, w, selected, diff)
	})
	if err != nil {
		return nil, err
	}
	return diff, nil
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

func (d *runDiff) payload() map[string]any {
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
		"users_created":       capList(d.UsersCreated),
		"users_deactivated":   capList(d.UsersDeactivated),
		"memberships_added":   capMap(d.MembershipsAdded),
		"memberships_removed": capMap(d.MembershipsRemoved),
		"groups_removed":      capList(d.GroupsRemoved),
	}
}

// recordRun writes one security audit entry for the run: what changed on
// success, why it failed otherwise.
func recordRun(orgID string, actor Actor, diff *runDiff, runErr error) {
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
	} else {
		row.RequestPayloadRedacted = diff.payload()
	}
	if err := models.CreateSecurityAuditLog(row); err != nil {
		log.With("org", orgID).Warnf("failed writing the slack import audit entry, reason=%v", err)
	}
}

// Start runs every organization's import on its interval until ctx is done.
// The control plane starts it; the gateway never does.
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
		log.Warnf("failed listing slack imports, reason=%v", err)
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
