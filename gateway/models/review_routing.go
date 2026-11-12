package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Review routing modes. Static means the AI routing fell back.
const (
	ReviewRoutingModeAI     = "ai"
	ReviewRoutingModeStatic = "static"
)

// Notify fallbacks: the routed notifications reached no user and no
// channel, so the plane tried to post the review as a review without
// routing is: in the listener's Slack channels or the default channel.
const (
	// ReviewRoutingNotifyNoIntegration: no notify key is integrated.
	ReviewRoutingNotifyNoIntegration = "no_integration"
	// ReviewRoutingNotifyNoTarget: the decision selected no reachable user
	// and no channel.
	ReviewRoutingNotifyNoTarget = "no_target"
	// ReviewRoutingNotifyNotDelivered: every post failed.
	ReviewRoutingNotifyNotDelivered = "not_delivered"
)

// ReviewRouting is the AI routing decision of a review (ENG-663). It holds
// opaque user ids only; an API response resolves the names.
type ReviewRouting struct {
	Mode           string                     `json:"mode"`
	FallbackCause  string                     `json:"fallback_cause,omitempty"`
	FallbackDetail string                     `json:"fallback_detail,omitempty"`
	Reason         string                     `json:"reason,omitempty"`
	Candidates     []string                   `json:"candidates"`
	Groups         []string                   `json:"groups"`
	Integrations   []ReviewRoutingIntegration `json:"integrations,omitempty"`
	NotifiedUsers  []ReviewRoutingUser        `json:"notified_users,omitempty"`
	Channels       []ReviewRoutingChannel     `json:"channels,omitempty"`
	Unreachable    []ReviewRoutingUnreachable `json:"unreachable,omitempty"`
	// UndeliveredChannels are the channels that did not get the review: a
	// configured candidate the integration does not reach from this
	// listener, or a post that failed.
	UndeliveredChannels []ReviewRoutingUndeliveredChannel `json:"undelivered_channels,omitempty"`
	// NotifyFallback is set when the routed notifications reached nobody
	// and the plane tried to post the review as without routing: a
	// ReviewRoutingNotify* value.
	NotifyFallback string `json:"notify_fallback,omitempty"`
	// NotifyFallbackPosted reports that a channel accepted that post. False
	// with NotifyFallback set: nobody was notified.
	NotifyFallbackPosted bool      `json:"notify_fallback_posted,omitempty"`
	DecidedAt            time.Time `json:"decided_at"`
}

// ReviewRoutingIntegration is one notify key of the routing configuration
// and its state when the decision was made.
type ReviewRoutingIntegration struct {
	Name       string `json:"name"`
	Delivery   string `json:"delivery"`
	Integrated bool   `json:"integrated"`
}

// ReviewRoutingUser is a user that got a direct message, and through which
// integrations.
type ReviewRoutingUser struct {
	ID           string   `json:"id"`
	Integrations []string `json:"integrations"`
}

// ReviewRoutingChannel is a channel that got the review.
type ReviewRoutingChannel struct {
	Integration string `json:"integration"`
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
}

// ReviewRoutingUnreachable is a selected user an integration could not reach.
type ReviewRoutingUnreachable struct {
	UserID      string `json:"user_id"`
	Integration string `json:"integration"`
	Cause       string `json:"cause"`
}

// ReviewRoutingUndeliveredChannel is a channel that did not get the review.
type ReviewRoutingUndeliveredChannel struct {
	Integration string `json:"integration"`
	ID          string `json:"id"`
	Cause       string `json:"cause"`
}

// Value stores the decision as JSONB.
func (r ReviewRouting) Value() (driver.Value, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan reads the decision from JSONB.
func (r *ReviewRouting) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		return nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("review routing: unsupported type %T", src)
	}
	return json.Unmarshal(b, r)
}

// UpdateReviewRouting replaces the routing decision of a review. The notify
// step calls it after it posted, to record who and where it reached.
func UpdateReviewRouting(db *gorm.DB, orgID, reviewID string, routing *ReviewRouting) error {
	res := db.Table("private.reviews").
		Where("org_id = ? AND id = ?", orgID, reviewID).
		UpdateColumn("routing", routing)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
