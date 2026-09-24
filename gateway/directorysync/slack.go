package directorysync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	slackservice "github.com/hoophq/hoop/gateway/slack"
)

// ErrSlackNotConfigured refuses a Slack import for an org whose Slack app is
// not running.
var ErrSlackNotConfigured = errors.New("the Slack integration is not configured or not running; set it up on the Slack page first")

// slackDirectory is the part of the Slack service the import reads.
type slackDirectory interface {
	ListUsers(ctx context.Context) ([]slackservice.DirectoryUser, error)
	ListUserGroups(ctx context.Context) ([]slackservice.UserGroup, error)
}

// openSlack returns the org's running Slack app. The import stores no
// credential: the app's bot token is the Slack integration's own. Tests
// replace it.
var openSlack = func(orgID string) (slackDirectory, error) {
	ss := slackservice.GetServiceInstance(orgID)
	if ss == nil {
		return nil, ErrSlackNotConfigured
	}
	return ss, nil
}

// workspace is one read of Slack: one users.list and one usergroups.list
// call, whatever the number of groups picked.
type workspace struct {
	users  map[string]slackservice.DirectoryUser
	groups map[string]slackservice.UserGroup
}

func readWorkspace(ctx context.Context, api slackDirectory) (*workspace, error) {
	users, err := api.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := api.ListUserGroups(ctx)
	if err != nil {
		return nil, err
	}
	w := &workspace{
		users:  make(map[string]slackservice.DirectoryUser, len(users)),
		groups: make(map[string]slackservice.UserGroup, len(groups)),
	}
	for _, u := range users {
		w.users[u.ID] = u
	}
	for _, g := range groups {
		if g.IsExternal {
			// A group shared from another organization is edited there.
			continue
		}
		w.groups[g.ID] = g
	}
	return w, nil
}

// groupName is the hoop group name of a user group: its handle, without the
// @. @dba-leads is the group dba-leads.
func groupName(g slackservice.UserGroup) string {
	return strings.TrimPrefix(g.Handle, "@")
}

// members returns the members Slack still vouches for: not deactivated, not a
// bot, not a guest and not from another organization.
func (w *workspace) members(g slackservice.UserGroup) []slackservice.DirectoryUser {
	var out []slackservice.DirectoryUser
	for _, id := range g.Users {
		if u, ok := w.users[id]; ok && vouched(u) {
			out = append(out, u)
		}
	}
	return out
}

func vouched(u slackservice.DirectoryUser) bool {
	return !u.Deleted && !u.IsBot && !u.IsRestricted && !u.IsUltraRestricted && !u.IsStranger
}

// selected returns the picked groups, or an error for a group that is gone.
// Who may edit a user group is a Slack workspace setting (ADR-0020): hoop
// trusts the groups as it trusts an identity provider's groups claim.
func (w *workspace) selected(groupIDs []string) ([]slackservice.UserGroup, error) {
	out := make([]slackservice.UserGroup, 0, len(groupIDs))
	for _, id := range groupIDs {
		g, ok := w.groups[id]
		if !ok {
			return nil, fmt.Errorf("slack user group %s no longer exists; remove it from the import", id)
		}
		out = append(out, g)
	}
	return out, nil
}

// Group is a Slack user group an admin can pick.
type Group struct {
	ID   string
	Name string
}

// ListGroups lists the org's Slack user groups, for choosing which to import.
func ListGroups(ctx context.Context, orgID string) ([]Group, error) {
	api, err := openSlack(orgID)
	if err != nil {
		return nil, err
	}
	w, err := readWorkspace(ctx, api)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(w.groups))
	for _, g := range w.groups {
		out = append(out, Group{ID: g.ID, Name: groupName(g)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
