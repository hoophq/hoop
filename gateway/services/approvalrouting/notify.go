package approvalrouting

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"gorm.io/gorm"
)

// Integration names, the keys of approvers.<level>.routing.notify.
const (
	IntegrationSlack = daemon.NotifySlack
	IntegrationTeams = daemon.NotifyTeams
)

// notifyTimeout bounds the notify step. Slack posts one message a second.
const notifyTimeout = 5 * time.Minute

// ChannelScope narrows the channels an integration reaches by default. A
// zero scope is the whole organization.
type ChannelScope struct {
	SidecarID    string
	ListenerName string
}

// Channel is one channel an integration can post to.
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ReviewCard is what a notification shows. It holds the display fields of
// today's review message.
type ReviewCard struct {
	ReviewID, SessionID string
	// Requester and RequesterEmail are who asked, as the message shows them.
	Requester, RequesterEmail string
	RequesterGroups           []string
	Connection                string
	ConnectionType            string
	Sidecar, Listener         string
	RiskLevel, Title          string
	Statement                 string
	ApprovalURL               string
	Groups                    []string
	ExpiresAt                 *time.Time
}

// Targets are where one integration posts.
type Targets struct {
	ChannelIDs  []string
	HoopUserIDs []string
}

// PostResult reports a post. Failed maps a channel id or a hoop user id
// onto the cause. Tried are the channel ids sent to the integration's API,
// accepted or not; a failure before it (no integration, too many groups,
// unread ids) tried none.
type PostResult struct {
	Posted int
	Failed map[string]string
	Tried  []string
}

// Notifier is one notification integration. Register one with
// RegisterNotifier; an integration without one is not integrated.
type Notifier interface {
	// Integrated reports the org set the integration up.
	Integrated(ctx context.Context, orgID string) bool
	// Channels lists the channels reachable in scope.
	Channels(ctx context.Context, db *gorm.DB, orgID string, scope ChannelScope) ([]Channel, error)
	// UsersReachable maps each user a direct message cannot reach onto the
	// cause. A user absent from the map is reachable.
	UsersReachable(ctx context.Context, db *gorm.DB, orgID string, hoopUserIDs []string) (map[string]string, error)
	// Post sends the review card to the targets.
	Post(ctx context.Context, db *gorm.DB, orgID string, card ReviewCard, t Targets) PostResult
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Notifier{}
)

// RegisterNotifier makes an integration available to routing. A later
// registration of the same name replaces it.
func RegisterNotifier(name string, n Notifier) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = n
}

// registered returns the notifier of name, or nil.
func registered(name string) Notifier {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[name]
}

// notifyPlan is who and where Notify posts.
type notifyPlan struct {
	orgID     string
	principal string
	excluded  []string
	users     []string
	routes    []*route
}

// plan binds the selected users to the routes.
func (rc *routeContext) plan(users []string) *notifyPlan {
	return &notifyPlan{orgID: rc.in.OrgID, principal: rc.in.Principal, excluded: slices.Clone(rc.excluded),
		users: users, routes: rc.routes}
}

// recipients are the planned users without the requester, resolved again:
// a user the principal names now is never sent a direct message. When the
// principal cannot be resolved, nobody is.
func (p *notifyPlan) recipients(ctx context.Context, db *gorm.DB, reviewID string) []string {
	ids, err := principalUsers(ctx, db, p.orgID, p.principal)
	if err != nil {
		log.With("review-id", reviewID).Warnf("no direct message is sent, %v", err)
		return nil
	}
	excluded := append(slices.Clone(p.excluded), ids...)
	var out []string
	for _, u := range p.users {
		if !slices.Contains(excluded, u) {
			out = append(out, u)
		}
	}
	return out
}

func (p *notifyPlan) route(integration string) *route {
	for _, r := range p.routes {
		if r.integration == integration {
			return r
		}
	}
	return nil
}

// Notify posts the review through the integrations of the decision and
// records who and where it reached. Call it after the review exists,
// detached from the request: posting can take minutes. unrouted posts the
// review as without routing and reports whether a channel accepted it;
// Notify calls it when the routed notifications reach no user and no
// channel, so a held statement is never announced less than without
// routing, and records the outcome. skip are the Slack channels the routed
// posts already sent to: a second post there would repeat a refusal, or
// duplicate a message whose answer was lost.
func Notify(ctx context.Context, db *gorm.DB, orgID string, d *RouteDecision, card ReviewCard,
	unrouted func(skip []string) bool) {
	if !d.Notifies() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()

	rec := *d.Record
	rec.NotifyFallback, rec.NotifyFallbackPosted = "", false
	rec.NotifiedUsers, rec.Channels, rec.Unreachable = nil, nil, nil
	rec.UndeliveredChannels = slices.Clone(d.Record.UndeliveredChannels)
	notified := map[string][]string{}
	integrated, posted := false, false
	var tried []string
	users := d.plan.recipients(ctx, db, card.ReviewID)
	for _, r := range d.plan.routes {
		dm := r.delivery != DeliveryChannel
		if r.notifier == nil {
			if dm {
				for _, u := range users {
					rec.Unreachable = append(rec.Unreachable, models.ReviewRoutingUnreachable{
						UserID: u, Integration: r.integration, Cause: r.integration + " is not integrated"})
				}
			}
			continue
		}
		integrated = true
		var t Targets
		if dm {
			t.HoopUserIDs = slices.Clone(users)
		}
		for _, ch := range r.channels {
			t.ChannelIDs = append(t.ChannelIDs, ch.ID)
		}
		if len(t.HoopUserIDs) == 0 && len(t.ChannelIDs) == 0 {
			continue
		}
		posted = true
		res := r.notifier.Post(ctx, db, orgID, card, t)
		if r.integration == IntegrationSlack {
			tried = append(tried, res.Tried...)
		}
		for _, u := range t.HoopUserIDs {
			if cause, failed := res.Failed[u]; failed {
				rec.Unreachable = append(rec.Unreachable, models.ReviewRoutingUnreachable{
					UserID: u, Integration: r.integration, Cause: boundText(cause, maxDetailRunes)})
				continue
			}
			notified[u] = append(notified[u], r.integration)
		}
		for _, ch := range r.channels {
			if cause, failed := res.Failed[ch.ID]; failed {
				log.With("review-id", card.ReviewID, "integration", r.integration, "channel", ch.ID).
					Warnf("the channel did not accept the routed review, %s", cause)
				rec.UndeliveredChannels = append(rec.UndeliveredChannels, models.ReviewRoutingUndeliveredChannel{
					Integration: r.integration, ID: ch.ID, Cause: boundText(cause, maxDetailRunes)})
				continue
			}
			rec.Channels = append(rec.Channels, ch)
		}
	}
	ids := make([]string, 0, len(notified))
	for id := range notified {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rec.NotifiedUsers = append(rec.NotifiedUsers, models.ReviewRoutingUser{ID: id, Integrations: notified[id]})
	}
	if len(rec.NotifiedUsers) == 0 && len(rec.Channels) == 0 {
		switch {
		case !integrated:
			rec.NotifyFallback = models.ReviewRoutingNotifyNoIntegration
		case !posted:
			rec.NotifyFallback = models.ReviewRoutingNotifyNoTarget
		default:
			rec.NotifyFallback = models.ReviewRoutingNotifyNotDelivered
		}
		log.With("review-id", card.ReviewID, "cause", rec.NotifyFallback).
			Warnf("the routed review reached no user and no channel, posting it as without routing")
		rec.NotifyFallbackPosted = unrouted(tried)
	}
	// Detached from ctx: the record must land even when the posts used the
	// whole budget.
	if err := models.UpdateReviewRouting(db.WithContext(context.WithoutCancel(ctx)), orgID, card.ReviewID, &rec); err != nil {
		log.With("review-id", card.ReviewID).Warnf("failed recording the routing notifications, reason=%v", err)
	}
}
