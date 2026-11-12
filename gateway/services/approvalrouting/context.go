package approvalrouting

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// Bounds of the approval history the model reads.
const (
	historyReviews = 100
	historyWindow  = 90 * 24 * time.Hour
)

// notifyUsersEnumLimit is the most eligible users the tool lists as an
// enum. Past it the schema takes any string and validation drops the rest.
const notifyUsersEnumLimit = 200

// routeContext is everything one routing step reads.
type routeContext struct {
	in         RouteInput
	candidates []string
	// groupMembers counts the active members of each candidate.
	groupMembers map[string]int
	// openReviews counts the pending reviews each candidate can decide.
	openReviews map[string]int
	// members are the active users of the candidates, requester removed,
	// by hoop user id.
	members map[string]member
	// excluded are the hoop user ids of the requester: RequesterID and every
	// user the principal names. Never routed to, never notified.
	excluded []string
	// requesterID and requesterGroups are what the model reads of the
	// requester: an opaque id and the groups.
	requesterID     string
	requesterGroups []string
	history         []historyEntry
	// routes are the notify keys, in name order.
	routes       []*route
	integrations []models.ReviewRoutingIntegration
	// unreachableChannels are the configured channel candidates the
	// integration does not reach in this listener's scope. Never offered.
	unreachableChannels []models.ReviewRoutingUndeliveredChannel
}

// causeChannelNotReachable is why a configured channel candidate is not
// offered: the integration does not list it for this listener.
const causeChannelNotReachable = "not reachable from this listener"

type member struct {
	groups []string
	// reachable lists the dm integrations that can reach the user.
	reachable []string
}

// historyEntry is one decision, or one missing decision, of a past review.
type historyEntry struct {
	ReviewerID      string `json:"reviewer_id,omitempty"`
	Group           string `json:"group"`
	Decision        string `json:"decision"`
	ResponseSeconds *int64 `json:"response_time_seconds,omitempty"`
	Date            string `json:"date"`
	RiskLevel       string `json:"risk_level,omitempty"`
}

// Decisions in the history.
const (
	decisionApproved   = "approved"
	decisionRejected   = "rejected"
	decisionNoResponse = "no_response"
)

// route is one notify key of the routing configuration.
type route struct {
	integration string
	delivery    string
	// notifier is nil when the integration is not integrated.
	notifier Notifier
	// allowed are the channels the model may select, by id.
	allowed map[string]models.ReviewRoutingChannel
	// order keeps allowed in a stable order for the prompt.
	order []string
	// channels are the selected channels, set by decide.
	channels []models.ReviewRoutingChannel
}

func parseOrgID(orgID string) (uuid.UUID, error) {
	id, err := uuid.Parse(orgID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid organization id %q: %w", orgID, err)
	}
	return id, nil
}

// candidateGroups are the configured candidates, else every group of the
// org, else the admin group.
func candidateGroups(ctx context.Context, db *gorm.DB, in RouteInput) ([]string, error) {
	if c := normalizeNames(in.Routing.Candidates); len(c) > 0 {
		return c, nil
	}
	var names []string
	err := db.WithContext(ctx).Raw(`SELECT DISTINCT name FROM private.user_groups WHERE org_id = ? ORDER BY name`,
		in.OrgID).Scan(&names).Error
	if err != nil {
		return nil, fmt.Errorf("failed reading the groups of the organization: %w", err)
	}
	if names = normalizeNames(names); len(names) > 0 {
		return names, nil
	}
	return defaultGroups(), nil
}

// load reads the rest of the routing context, after the candidates. Nothing
// it keeps is a name or an email.
func (rc *routeContext) load(ctx context.Context, db *gorm.DB) error {
	if err := rc.loadRequester(ctx, db); err != nil {
		return err
	}
	if err := rc.loadMembers(ctx, db); err != nil {
		return err
	}
	if err := rc.loadOpenReviews(ctx, db); err != nil {
		return err
	}
	if err := rc.loadHistory(ctx, db, time.Now().UTC()); err != nil {
		return err
	}
	return rc.loadRoutes(ctx, db)
}

// principalUsers returns the ids of the org users the principal names: by
// id, by subject, or by email ignoring case. Any status: a user named by the
// principal is the requester whatever its status.
func principalUsers(ctx context.Context, db *gorm.DB, orgID, principal string) ([]string, error) {
	if principal == "" {
		return nil, nil
	}
	var ids []string
	err := db.WithContext(ctx).Raw(`
		SELECT id::text FROM private.users
		WHERE org_id = ? AND (id::text = ? OR subject = ? OR lower(email) = lower(?))
		ORDER BY id`, orgID, principal, principal, principal).Scan(&ids).Error
	if err != nil {
		return nil, fmt.Errorf("failed resolving the requester of the approval request: %w", err)
	}
	return ids, nil
}

// loadRequester resolves who asked: RequesterID and the users the principal
// names are excluded. Without a RequesterID, the first mapped user is the
// requester the model reads, with its groups.
func (rc *routeContext) loadRequester(ctx context.Context, db *gorm.DB) error {
	ids, err := principalUsers(ctx, db, rc.in.OrgID, rc.in.Principal)
	if err != nil {
		return err
	}
	rc.excluded = normalizeNames(append([]string{rc.in.RequesterID}, ids...))
	rc.requesterID, rc.requesterGroups = rc.in.RequesterID, rc.in.RequesterGroups
	if rc.requesterID != "" || len(ids) == 0 {
		return nil
	}
	rc.requesterID = ids[0]
	groups, err := models.GetUserGroupsByUserIDs(db.WithContext(ctx), rc.in.OrgID, ids[:1])
	if err != nil {
		return fmt.Errorf("failed reading the groups of the requester: %w", err)
	}
	for _, g := range groups {
		rc.requesterGroups = append(rc.requesterGroups, g.Name)
	}
	rc.requesterGroups = normalizeNames(rc.requesterGroups)
	return nil
}

// loadMembers reads the active users of the candidates.
func (rc *routeContext) loadMembers(ctx context.Context, db *gorm.DB) error {
	var rows []struct {
		ID    string `gorm:"column:id"`
		Group string `gorm:"column:name"`
	}
	err := db.WithContext(ctx).Raw(`
		SELECT u.id, ug.name
		FROM private.users u
		JOIN private.user_groups ug ON ug.user_id = u.id AND ug.org_id = u.org_id
		WHERE u.org_id = ? AND u.status = 'active' AND ug.name IN ?
		ORDER BY u.id, ug.name`, rc.in.OrgID, rc.candidates).Scan(&rows).Error
	if err != nil {
		return fmt.Errorf("failed reading the members of the candidate groups: %w", err)
	}
	rc.groupMembers = map[string]int{}
	rc.members = map[string]member{}
	for _, r := range rows {
		rc.groupMembers[r.Group]++
		if slices.Contains(rc.excluded, r.ID) {
			continue
		}
		m := rc.members[r.ID]
		if !slices.Contains(m.groups, r.Group) {
			m.groups = append(m.groups, r.Group)
		}
		rc.members[r.ID] = m
	}
	return nil
}

// loadOpenReviews counts the pending reviews each candidate can decide: the
// load of its members.
func (rc *routeContext) loadOpenReviews(ctx context.Context, db *gorm.DB) error {
	var rows []struct {
		Group string `gorm:"column:group_name"`
		Count int    `gorm:"column:open"`
	}
	err := db.WithContext(ctx).Raw(`
		SELECT rg.group_name, COUNT(DISTINCT rv.id) AS open
		FROM private.reviews rv
		JOIN private.review_groups rg ON rg.review_id = rv.id
		WHERE rv.org_id = ? AND rv.status = 'PENDING' AND rg.status = 'PENDING' AND rg.group_name IN ?
		AND NOT (COALESCE(rv.listener_name, '') <> '' AND rv.expires_at IS NOT NULL AND rv.expires_at <= ?)
		GROUP BY rg.group_name`, rc.in.OrgID, rc.candidates, time.Now().UTC()).Scan(&rows).Error
	if err != nil {
		return fmt.Errorf("failed counting the open reviews: %w", err)
	}
	rc.openReviews = map[string]int{}
	for _, r := range rows {
		rc.openReviews[r.Group] = r.Count
	}
	return nil
}

// loadHistory reads the decisions of the last historyReviews decided
// reviews of this sidecar and listener, inside historyWindow.
func (rc *routeContext) loadHistory(ctx context.Context, db *gorm.DB, now time.Time) error {
	if rc.in.SidecarID == "" || rc.in.ListenerName == "" {
		return nil
	}
	var rows []struct {
		ReviewStatus string     `gorm:"column:review_status"`
		CreatedAt    time.Time  `gorm:"column:created_at"`
		RiskLevel    *string    `gorm:"column:risk_level"`
		Group        string     `gorm:"column:group_name"`
		GroupStatus  string     `gorm:"column:group_status"`
		ReviewerID   *string    `gorm:"column:reviewer_id"`
		ReviewedAt   *time.Time `gorm:"column:reviewed_at"`
	}
	// review_groups.owner_id holds the reviewer's auth subject, which can be
	// an email. Only the org user's id reaches the model; a reviewer with no
	// user keeps the entry without an id.
	err := db.WithContext(ctx).Raw(`
		WITH recent AS (
			SELECT id, status, created_at, risk_level, expires_at
			FROM private.reviews
			WHERE org_id = ? AND sidecar_id = ? AND listener_name = ? AND created_at >= ?
			AND (status <> 'PENDING' OR (expires_at IS NOT NULL AND expires_at <= ?))
			ORDER BY created_at DESC
			LIMIT ?
		)
		SELECT r.status AS review_status, r.created_at, r.risk_level,
			rg.group_name, rg.status AS group_status, ru.id AS reviewer_id, rg.reviewed_at
		FROM recent r
		JOIN private.review_groups rg ON rg.review_id = r.id
		LEFT JOIN LATERAL (
			SELECT u.id::text AS id FROM private.users u
			WHERE u.org_id = ? AND rg.owner_id IS NOT NULL
			AND (u.id::text = rg.owner_id::text OR u.subject = rg.owner_id::text)
			ORDER BY (u.id::text = rg.owner_id::text) DESC, u.id
			LIMIT 1
		) ru ON true
		ORDER BY r.created_at DESC, rg.group_name`,
		rc.in.OrgID, rc.in.SidecarID, rc.in.ListenerName, now.Add(-historyWindow), now, historyReviews,
		rc.in.OrgID).
		Scan(&rows).Error
	if err != nil {
		return fmt.Errorf("failed reading the approval history: %w", err)
	}
	for _, r := range rows {
		e := historyEntry{Group: r.Group, Date: r.CreatedAt.UTC().Format(time.RFC3339)}
		if r.RiskLevel != nil {
			e.RiskLevel = *r.RiskLevel
		}
		switch models.ReviewStatusType(r.GroupStatus) {
		case models.ReviewStatusApproved:
			e.Decision = decisionApproved
		case models.ReviewStatusRejected, models.ReviewStatusRevoked:
			e.Decision = decisionRejected
		case models.ReviewStatusPending:
			// A group nobody from answered counts only when the review
			// expired: in a decided review it was not needed.
			if r.ReviewStatus != string(models.ReviewStatusExpired) && r.ReviewStatus != string(models.ReviewStatusPending) {
				continue
			}
			e.Decision = decisionNoResponse
		default:
			continue
		}
		if e.Decision != decisionNoResponse {
			if r.ReviewerID != nil {
				e.ReviewerID = *r.ReviewerID
			}
			if r.ReviewedAt != nil {
				secs := int64(r.ReviewedAt.Sub(r.CreatedAt) / time.Second)
				if secs < 0 {
					secs = 0
				}
				e.ResponseSeconds = &secs
			}
		}
		rc.history = append(rc.history, e)
	}
	return nil
}

// loadRoutes resolves each notify key: integrated or not, its delivery and
// the channels the model may select.
func (rc *routeContext) loadRoutes(ctx context.Context, db *gorm.DB) error {
	names := make([]string, 0, len(rc.in.Routing.Notify))
	for name := range rc.in.Routing.Notify {
		names = append(names, name)
	}
	sort.Strings(names)
	scope := ChannelScope{SidecarID: rc.in.SidecarID, ListenerName: rc.in.ListenerName}
	for _, name := range names {
		// Another key is ignored, as the sidecar warns.
		if name != IntegrationSlack && name != IntegrationTeams {
			continue
		}
		cfg := rc.in.Routing.Notify[name]
		r := &route{integration: name, delivery: cfg.EffectiveDelivery(),
			allowed: map[string]models.ReviewRoutingChannel{}}
		if n := registered(name); n != nil && n.Integrated(ctx, rc.in.OrgID) {
			r.notifier = n
		}
		rc.integrations = append(rc.integrations, models.ReviewRoutingIntegration{
			Name: name, Delivery: r.delivery, Integrated: r.notifier != nil})
		rc.routes = append(rc.routes, r)
		if r.notifier == nil {
			continue
		}
		if r.delivery != DeliveryChannel && len(rc.members) > 0 {
			causes, err := r.notifier.UsersReachable(ctx, db, rc.in.OrgID, rc.eligibleUsers())
			if err != nil {
				return fmt.Errorf("failed checking %s reachability: %w", name, err)
			}
			for id, m := range rc.members {
				if _, unreachable := causes[id]; !unreachable {
					m.reachable = append(m.reachable, name)
					rc.members[id] = m
				}
			}
		}
		if r.delivery == DeliveryDM {
			continue
		}
		reachable, err := r.notifier.Channels(ctx, db, rc.in.OrgID, scope)
		if err != nil {
			return fmt.Errorf("failed listing the %s channels: %w", name, err)
		}
		channelNames := map[string]string{}
		var reachableIDs []string
		for _, ch := range reachable {
			channelNames[ch.ID] = ch.Name
			reachableIDs = append(reachableIDs, ch.ID)
		}
		// Only a channel the integration reaches in this listener's scope is
		// offered. A configured candidate outside it is recorded instead: it
		// can be stale, or a channel another listener's approvers read.
		ids := normalizeNames(cfg.ChannelsCandidates)
		if len(ids) == 0 {
			ids = reachableIDs
		}
		for _, id := range normalizeNames(ids) {
			if _, ok := channelNames[id]; !ok {
				rc.unreachableChannels = append(rc.unreachableChannels, models.ReviewRoutingUndeliveredChannel{
					Integration: name, ID: id, Cause: causeChannelNotReachable})
				continue
			}
			r.allowed[id] = models.ReviewRoutingChannel{Integration: name, ID: id, Name: channelNames[id]}
			r.order = append(r.order, id)
		}
	}
	return nil
}

// eligibleUsers are the members, in id order.
func (rc *routeContext) eligibleUsers() []string {
	ids := make([]string, 0, len(rc.members))
	for id := range rc.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// channelRoutes are the integrated routes that post to channels and have one.
func (rc *routeContext) channelRoutes() []*route {
	var out []*route
	for _, r := range rc.routes {
		if r.notifier != nil && r.delivery != DeliveryDM && len(r.order) > 0 {
			out = append(out, r)
		}
	}
	return out
}
