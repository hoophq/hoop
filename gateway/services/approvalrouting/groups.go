package approvalrouting

import (
	"context"
	"fmt"
	"slices"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// NotifyGroups plans the notifications of a review filed to static groups
// (approvers.<level>.notify). No model is asked, so nothing selects:
// in.Routing.Candidates are the groups of the review, empty for the
// administrators; every member of them that may decide gets the direct
// message of a dm route; and a channel route posts to its configured
// channels, or to what a review without notify posts to (staticChannels).
//
// Notify then posts and records it, as it does for a routed review. A route
// with delivery dm never reaches a channel, also when it reaches nobody.
//
// It returns an error when the members or the routes cannot be read; the
// caller then files nothing. A decision with no known notify key does not
// notify: the caller notifies as it does without notify.
func NotifyGroups(ctx context.Context, db *gorm.DB, in RouteInput) (*RouteDecision, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	var groups []string
	if in.IncludeSidecarOwners {
		groups = append(groups, models.ReviewGroupSidecarOwners)
		for _, g := range normalizeNames(in.Routing.Candidates) {
			if g != models.ReviewGroupSidecarOwners {
				groups = append(groups, g)
			}
		}
	} else {
		groups = normalizeNames(in.Routing.Candidates)
		if len(groups) == 0 {
			groups = defaultGroups()
		}
	}
	rc := &routeContext{in: in, candidates: groups}
	if err := rc.loadRequester(ctx, db); err != nil {
		return nil, err
	}
	if err := rc.loadMembers(ctx, db); err != nil {
		return nil, err
	}
	if in.IncludeSidecarOwners {
		if err := rc.loadSidecarOwners(ctx, db); err != nil {
			return nil, err
		}
	}
	if err := rc.loadRoutes(ctx, db); err != nil {
		return nil, err
	}

	plan := rc.plan(rc.eligibleUsers(), groups)
	plan.noChannelFallback = in.NoDefaultChannel
	for _, r := range plan.routes {
		ids, err := rc.staticChannels(ctx, db, r)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			r.channels = append(r.channels, r.allowed[id])
		}
	}
	d := &RouteDecision{Groups: groups, Record: rc.record(models.ReviewRoutingModeGroups, groups)}
	if len(rc.routes) > 0 {
		d.plan = plan
	}
	return d, nil
}

// loadSidecarOwners adds every unambiguously matched owner of the sidecar to
// rc.members under models.ReviewGroupSidecarOwners, and logs any owner that
// matches no single Hoop user.
func (rc *routeContext) loadSidecarOwners(ctx context.Context, db *gorm.DB) error {
	ownerIDs, err := models.ListSidecarOwnerUserIDs(db.WithContext(ctx), rc.in.OrgID, rc.in.SidecarID)
	if err != nil {
		return fmt.Errorf("failed reading the owners of the sidecar: %w", err)
	}
	for _, id := range ownerIDs {
		if slices.Contains(rc.excluded, id) {
			continue
		}
		rc.groupMembers[models.ReviewGroupSidecarOwners]++
		m := rc.members[id]
		if !slices.Contains(m.groups, models.ReviewGroupSidecarOwners) {
			m.groups = append(m.groups, models.ReviewGroupSidecarOwners)
		}
		rc.members[id] = m
	}
	owners, err := models.ListSidecarOwners(db.WithContext(ctx), rc.in.OrgID, rc.in.SidecarID)
	if err == nil {
		for _, o := range owners {
			if !o.Matched {
				log.With("org", rc.in.OrgID, "sidecar", rc.in.SidecarID).
					Warnf("sidecar owner %s matches no single Hoop user and cannot be notified", o.Email)
			}
		}
	}
	return nil
}

// staticChannels are the channels a route posts to when nothing selects.
// With channels_candidates, every one the integration reaches. Without, what
// a review without notify posts to: the listener's Slack channels, or the
// org default channel when the listener has none.
func (rc *routeContext) staticChannels(ctx context.Context, db *gorm.DB, r *route) ([]string, error) {
	if r.notifier == nil || r.delivery == DeliveryDM {
		return nil, nil
	}
	configured := normalizeNames(rc.in.Routing.Notify[r.integration].ChannelsCandidates)
	if rc.in.NoDefaultChannel {
		if len(configured) == 0 {
			return nil, nil
		}
		return r.order, nil
	}
	if len(configured) > 0 || r.integration != IntegrationSlack {
		return r.order, nil
	}
	listener, err := models.ResolveSidecarSlackChannels(db.WithContext(ctx), rc.in.OrgID, rc.in.SidecarID, rc.in.ListenerName)
	if err != nil {
		return nil, fmt.Errorf("failed reading the listener slack channels: %w", err)
	}
	var own []string
	for _, id := range normalizeNames(listener) {
		if _, ok := r.allowed[id]; ok {
			own = append(own, id)
		}
	}
	if len(own) > 0 {
		return own, nil
	}
	return r.order, nil
}
