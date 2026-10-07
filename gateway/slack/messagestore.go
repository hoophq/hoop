package slack

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/slack-go/slack"
	"gorm.io/gorm"
)

// messageStore tracks the review messages a SlackService posted, and the
// terminal rewrite each review got.
//
// Two implementations with one contract. memoryMessageStore lives in process;
// dbMessageStore is shared by every replica of an org in replica mode.
type messageStore interface {
	// track records a posted message. It returns the terminal state instead
	// when the review already settled, for the caller to rewrite it with.
	track(reviewID string, m sentReviewMessage) (*UpdateReviewMessageRequest, error)
	// settled returns the review's terminal state, or nil.
	settled(reviewID string) (*UpdateReviewMessageRequest, error)
	// hasTracked reports whether update would rewrite anything for a review
	// that has not settled.
	hasTracked(reviewID string) (bool, error)
	// update returns the messages req rewrites, and records req when it is
	// terminal. A revoke or an expiry rewrites what an approval did; any
	// other request after a terminal one rewrites nothing.
	update(req *UpdateReviewMessageRequest) ([]sentReviewMessage, error)
}

// settledReview is the terminal state UpdateReviewMessage applied.
type settledReview struct {
	req *UpdateReviewMessageRequest
	at  time.Time
	// items are the messages an approval rewrote, kept so a revoke or an expiry
	// can rewrite them again. Empty for every other terminal state.
	items []sentReviewMessage
	// deadline is the approval deadline, so the items outlive it.
	deadline *time.Time
}

// trackedUntil: from + sentReviewRetention, counted from the deadline when it is later.
func trackedUntil(from time.Time, deadline *time.Time) time.Time {
	if deadline != nil && deadline.After(from) {
		from = *deadline
	}
	return from.Add(sentReviewRetention)
}

// memoryMessageStore keeps the messages in process. Entries are removed on
// terminal updates and expire after trackedUntil. The zero value is
// ready to use.
type memoryMessageStore struct {
	mu    sync.Mutex
	items map[string][]sentReviewMessage
	// settledReviews keeps the terminal rewrite of a review, so a message a
	// post loop still sends after the review settled is rewritten, and the
	// loop posts no more active buttons.
	settledReviews map[string]settledReview
}

func (s *memoryMessageStore) track(reviewID string, m sentReviewMessage) (*UpdateReviewMessageRequest, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	// lazy eviction keeps the maps bounded without a janitor goroutine
	for id, items := range s.items {
		if len(items) > 0 && now.After(trackedUntil(items[0].sentAt, items[0].deadline)) {
			delete(s.items, id)
		}
	}
	for id, sr := range s.settledReviews {
		if now.After(trackedUntil(sr.at, sr.deadline)) {
			delete(s.settledReviews, id)
		}
	}
	if sr, ok := s.settledReviews[reviewID]; ok {
		return sr.req, nil
	}
	if s.items == nil {
		s.items = make(map[string][]sentReviewMessage)
	}
	s.items[reviewID] = append(s.items[reviewID], m)
	return nil, nil
}

func (s *memoryMessageStore) settled(reviewID string) (*UpdateReviewMessageRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sr, ok := s.settledReviews[reviewID]; ok {
		return sr.req, nil
	}
	return nil, nil
}

func (s *memoryMessageStore) hasTracked(reviewID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items[reviewID]) > 0, nil
}

func (s *memoryMessageStore) update(req *UpdateReviewMessageRequest) ([]sentReviewMessage, error) {
	done := req.isDone()
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.items[req.ReviewID]
	if req.IsRevoked || req.IsExpired {
		// An approval already consumed the tracked messages.
		items = append(items, s.settledReviews[req.ReviewID].items...)
	}
	if done {
		delete(s.items, req.ReviewID)
		if s.settledReviews == nil {
			s.settledReviews = make(map[string]settledReview)
		}
		settled := settledReview{req: req, at: time.Now().UTC()}
		if req.IsApproved {
			settled.items = items
			settled.deadline = req.ExpiresAt
		}
		s.settledReviews[req.ReviewID] = settled
	}
	return items, nil
}

// dbMessageStore keeps the messages in the database, so the replica that
// rewrites a message need not be the one that posted it.
//
// A post and a settlement can run on two replicas at once. Each writes first
// and reads the other's table second (track: insert, then read the
// settlement; update: write the settlement, then read the messages), each as
// its own statement. Whichever statement commits last sees the other's write,
// so a message posted while its review settled is rewritten by at least one
// of the two.
type dbMessageStore struct {
	db    *gorm.DB
	orgID string
}

func (s *dbMessageStore) track(reviewID string, m sentReviewMessage) (*UpdateReviewMessageRequest, error) {
	blocks, err := json.Marshal(slack.Blocks{BlockSet: m.blocks})
	if err != nil {
		return nil, fmt.Errorf("encoding the review message blocks: %w", err)
	}
	err = models.InsertSlackReviewMessage(s.db, models.SlackReviewMessage{
		ReviewID: reviewID, OrgID: s.orgID, ChannelID: m.channelID, Timestamp: m.timestamp,
		EventKind: m.eventKind, Blocks: blocks, SentAt: m.sentAt, Deadline: m.deadline,
	}, sentReviewRetention)
	if err != nil {
		return nil, err
	}
	return s.settled(reviewID)
}

func (s *dbMessageStore) settled(reviewID string) (*UpdateReviewMessageRequest, error) {
	st, err := s.settlement(reviewID)
	if st == nil || err != nil {
		return nil, err
	}
	var req UpdateReviewMessageRequest
	if err := json.Unmarshal(st.Request, &req); err != nil {
		return nil, fmt.Errorf("decoding the review settlement: %w", err)
	}
	return &req, nil
}

func (s *dbMessageStore) hasTracked(reviewID string) (bool, error) {
	st, err := s.settlement(reviewID)
	if st != nil || err != nil {
		return false, err
	}
	items, err := models.ListSlackReviewMessages(s.db, reviewID)
	return len(items) > 0, err
}

func (s *dbMessageStore) update(req *UpdateReviewMessageRequest) ([]sentReviewMessage, error) {
	prev, err := s.settlement(req.ReviewID)
	if err != nil {
		return nil, err
	}
	if req.isDone() {
		encoded, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("encoding the review settlement: %w", err)
		}
		st := models.SlackReviewSettlement{ReviewID: req.ReviewID, OrgID: s.orgID, Request: encoded,
			Rewritable: req.IsApproved}
		if req.IsApproved {
			st.Deadline = req.ExpiresAt
		}
		applied, err := models.UpsertSlackReviewSettlement(s.db, st)
		if err != nil {
			return nil, err
		}
		// The review was revoked or expired first, and this request lost the
		// race: rewriting would put the message back to its old state.
		if !applied {
			return nil, nil
		}
		// An expiry rewrites these messages at the approval deadline.
		if st.Deadline != nil {
			if err := models.ExtendSlackReviewMessagesDeadline(s.db, req.ReviewID, *st.Deadline); err != nil {
				return nil, err
			}
		}
	}
	if prev != nil && !((req.IsRevoked || req.IsExpired) && prev.Rewritable) {
		return nil, nil
	}
	rows, err := models.ListSlackReviewMessages(s.db, req.ReviewID)
	if err != nil {
		return nil, err
	}
	items := make([]sentReviewMessage, 0, len(rows))
	for _, r := range rows {
		var blocks slack.Blocks
		if err := json.Unmarshal(r.Blocks, &blocks); err != nil {
			return nil, fmt.Errorf("decoding the blocks of review message %s/%s: %w", r.ChannelID, r.Timestamp, err)
		}
		items = append(items, sentReviewMessage{channelID: r.ChannelID, timestamp: r.Timestamp,
			eventKind: r.EventKind, blocks: blocks.BlockSet, sentAt: r.SentAt, deadline: r.Deadline})
	}
	return items, nil
}

// settlement returns the review's settlement, or nil when it has not settled.
func (s *dbMessageStore) settlement(reviewID string) (*models.SlackReviewSettlement, error) {
	st, err := models.GetSlackReviewSettlement(s.db, reviewID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return st, err
}
