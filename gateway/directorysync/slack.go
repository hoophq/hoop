package directorysync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	slackservice "github.com/hoophq/hoop/gateway/slack"
)

// ErrSlackNotConfigured refuses a Slack sync for an org whose Slack app is not
// running.
var ErrSlackNotConfigured = errors.New("the Slack integration is not configured or not running; set it up on the Slack page first")

// slackDirectory is the part of the Slack service the import reads.
type slackDirectory interface {
	ListUsers(ctx context.Context) ([]slackservice.DirectoryUser, error)
	ListUserGroups(ctx context.Context) ([]slackservice.UserGroup, error)
}

// slackProvider reads Slack user groups as the directory. Group names are the
// user group handle: @dba-leads is the hoop group dba-leads.
//
// One run makes one users.list and one usergroups.list call, whatever the
// number of groups picked.
type slackProvider struct {
	api slackDirectory

	once    sync.Once
	loadErr error
	users   map[string]slackservice.DirectoryUser
	groups  map[string]slackservice.UserGroup
}

// NewSlackProvider reads the org's running Slack app. It stores no credential:
// the app's bot token is the Slack integration's own.
func NewSlackProvider(orgID string) (Provider, error) {
	ss := slackservice.GetServiceInstance(orgID)
	if ss == nil {
		return nil, ErrSlackNotConfigured
	}
	return &slackProvider{api: ss}, nil
}

func (p *slackProvider) load(ctx context.Context) error {
	p.once.Do(func() {
		users, err := p.api.ListUsers(ctx)
		if err != nil {
			p.loadErr = err
			return
		}
		groups, err := p.api.ListUserGroups(ctx)
		if err != nil {
			p.loadErr = err
			return
		}
		p.users = make(map[string]slackservice.DirectoryUser, len(users))
		for _, u := range users {
			p.users[u.ID] = u
		}
		p.groups = make(map[string]slackservice.UserGroup, len(groups))
		for _, g := range groups {
			if g.IsExternal {
				// A group shared from another organization is edited there.
				continue
			}
			p.groups[g.ID] = g
		}
	})
	return p.loadErr
}

func (p *slackProvider) ListGroups(ctx context.Context) ([]Group, error) {
	if err := p.load(ctx); err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(p.groups))
	for _, g := range p.groups {
		out = append(out, Group{ID: g.ID, Name: strings.TrimPrefix(g.Handle, "@")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListMembers returns the members Slack still vouches for: not deactivated,
// not a bot, not a guest and not from another organization.
func (p *slackProvider) ListMembers(ctx context.Context, groupID string) ([]User, error) {
	if err := p.load(ctx); err != nil {
		return nil, err
	}
	g, ok := p.groups[groupID]
	if !ok {
		return nil, fmt.Errorf("slack user group %s not found", groupID)
	}
	var out []User
	for _, id := range g.Users {
		u, ok := p.users[id]
		if !ok || !vouched(u) {
			continue
		}
		out = append(out, User{
			ExternalID: u.ID,
			Email:      strings.ToLower(strings.TrimSpace(u.Email)),
			Name:       u.Name,
		})
	}
	return out, nil
}

func vouched(u slackservice.DirectoryUser) bool {
	return !u.Deleted && !u.IsBot && !u.IsRestricted && !u.IsUltraRestricted && !u.IsStranger
}

// CheckGroups refuses a user group whose last editor is not a workspace admin
// or owner. hoop cannot restrict who edits user groups, and Slack does not say
// whether the identity provider manages a group, so the last editor is the
// only signal. allowMemberManaged is the admin's explicit opt out.
func (p *slackProvider) CheckGroups(ctx context.Context, groupIDs []string, allowMemberManaged bool) error {
	if allowMemberManaged {
		return nil
	}
	if err := p.load(ctx); err != nil {
		return err
	}
	var refused []string
	for _, id := range groupIDs {
		g, ok := p.groups[id]
		if !ok {
			continue
		}
		editor := g.UpdatedBy
		if editor == "" {
			editor = g.CreatedBy
		}
		if e, ok := p.users[editor]; ok && (e.IsAdmin || e.IsOwner) {
			continue
		}
		refused = append(refused, "@"+strings.TrimPrefix(g.Handle, "@"))
	}
	if len(refused) == 0 {
		return nil
	}
	sort.Strings(refused)
	return fmt.Errorf("user group(s) %s were last edited by a member who is not a Slack workspace admin or owner; "+
		"restrict user group editing to admins in the Slack workspace settings, or allow member-managed groups",
		strings.Join(refused, ", "))
}

// DeletedUsers returns the Slack ids of deactivated members.
func (p *slackProvider) DeletedUsers(ctx context.Context) (map[string]bool, error) {
	if err := p.load(ctx); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for id, u := range p.users {
		if u.Deleted {
			out[id] = true
		}
	}
	return out, nil
}

func (p *slackProvider) LinksSlackID() bool { return true }

// GovernedGroup is a group with whether its last editor may be trusted.
type GovernedGroup struct {
	Group
	AdminManaged bool
}

// ListGroupsWithGovernance lists the provider's groups, each marked with
// whether the run would accept it without allowing member-managed groups.
func ListGroupsWithGovernance(ctx context.Context, p Provider) ([]GovernedGroup, error) {
	groups, err := p.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	guard, hasGuard := p.(groupGuard)
	out := make([]GovernedGroup, 0, len(groups))
	for _, g := range groups {
		adminManaged := true
		if hasGuard {
			adminManaged = guard.CheckGroups(ctx, []string{g.ID}, false) == nil
		}
		out = append(out, GovernedGroup{Group: g, AdminManaged: adminManaged})
	}
	return out, nil
}
