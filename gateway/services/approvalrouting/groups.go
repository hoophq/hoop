package approvalrouting

import (
	"context"
	"fmt"

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

	groups := normalizeNames(in.Routing.Candidates)
	if len(groups) == 0 {
		groups = defaultGroups()
	}
	rc := &routeContext{in: in, candidates: groups}
	if err := rc.loadRequester(ctx, db); err != nil {
		return nil, err
	}
	if err := rc.loadMembers(ctx, db); err != nil {
		return nil, err
	}
	if err := rc.loadRoutes(ctx, db); err != nil {
		return nil, err
	}

	plan := rc.plan(rc.eligibleUsers(), groups)
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

// staticChannels are the channels a route posts to when nothing selects.
// With channels_candidates, every one the integration reaches. Without, what
// a review without notify posts to: the listener's Slack channels, or the
// org default channel when the listener has none.
func (rc *routeContext) staticChannels(ctx context.Context, db *gorm.DB, r *route) ([]string, error) {
	if r.notifier == nil || r.delivery == DeliveryDM {
		return nil, nil
	}
	configured := normalizeNames(rc.in.Routing.Notify[r.integration].ChannelsCandidates)
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
