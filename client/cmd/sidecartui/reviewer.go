package sidecartui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// Review statuses, spelled as the control plane spells them, because the
// analyzer words its denial from them (analyzer.reviewReason).
const (
	statusPending  = "PENDING"
	statusApproved = "APPROVED"
	statusRejected = "REJECTED"
	statusExecuted = "EXECUTED"
	// statusExpired is a review nobody may use any more: pending past the
	// time a hold waits, or approved and not used in that time. The
	// analyzer reads an unknown status as "not released", which is right.
	statusExpired = "EXPIRED"
)

// maxSettled bounds the settled reviews kept for the Approvals section.
const maxSettled = 300

// maxPending bounds the reviews waiting on the person at once. Past it a new
// hold is refused (the analyzer denies it) rather than queued without end:
// nobody decides fifty statements in a row, and each one holds its bytes.
const maxPending = 50

// maxStatementBytes is the largest statement filed for review: the control
// plane's own limit, so a statement is refused the same way on both paths.
const maxStatementBytes = 100 << 10

// reviewLife is how long a review stays usable: the time a held client waits
// (analyzer.ReviewWait). After it, a pending review has nobody waiting on it,
// and an approval nobody used is stale; both expire, and a resend files anew.
var reviewLife = analyzer.ReviewWait

// LocalReview is one statement held for the person at the terminal.
type LocalReview struct {
	ID        string
	Listener  string
	Statement string
	Status    string
	Filed     time.Time
	Expired   bool
	DecidedBy string
	Decided   time.Time

	// Why the analyzer held it (analyzer.HoldDetail): the level, the
	// model's one-line title and its explanation, and the rule. Shown to
	// the person deciding, who already sees the statement itself.
	Risk  string
	Title string
	Why   string
	Rule  string
}

// Reviewer is the review backend for a sidecar with no control plane: the
// person running `hoop start sidecar` in a terminal approves or rejects each
// held statement in the TUI. It is passed to daemon.WithLocalReviewer.
//
// It follows the control plane's contract, because the analyzer's hold loop
// is written against it:
//
//   - File answers from the review already filed for the same listener and
//     the same bytes while it is pending or approved, so a client in review
//     mode "return" resends the identical statement and gets the approval.
//   - An approval releases exactly one statement. The File or Claim that
//     spends it gets Forward; every later ask reads EXECUTED, and a resend
//     after that files a new review.
//   - A rejection is final for that review. A resend files a new one, the
//     same as on the plane, so the person sees it again.
//
// File and Claim run on the data path, inside a client's statement, so they
// never wait on the screen: a change only raises a flag (Changed), and the
// TUI reads a Snapshot when it can. A terminal frozen with Ctrl-S delays the
// dialog, never the statement an approval releases.
type Reviewer struct {
	mu      sync.Mutex
	reviews map[string]*LocalReview
	// open maps listener and statement to the review a resend answers from.
	open    map[string]string
	changed chan struct{}
	now     func() time.Time
}

// NewReviewer returns an empty reviewer.
func NewReviewer() *Reviewer {
	return &Reviewer{
		reviews: map[string]*LocalReview{},
		open:    map[string]string{},
		changed: make(chan struct{}, 1),
		now:     time.Now,
	}
}

// For is the daemon.LocalReviewer: the backend one lane files with.
func (r *Reviewer) For(listener string) analyzer.Reviewer {
	return laneReviewer{r: r, listener: listener}
}

var _ daemon.LocalReviewer = (*Reviewer)(nil).For

// Changed receives after a review is filed, decided or spent. One signal may
// stand for several changes; read Snapshot for the state.
func (r *Reviewer) Changed() <-chan struct{} { return r.changed }

// Snapshot returns every review kept, oldest first.
func (r *Reviewer) Snapshot() []LocalReview {
	r.mu.Lock()
	out := make([]LocalReview, 0, len(r.reviews))
	for _, rv := range r.reviews {
		out = append(out, *rv)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Filed.Equal(out[j].Filed) {
			return out[i].ID < out[j].ID
		}
		return out[i].Filed.Before(out[j].Filed)
	})
	return out
}

type laneReviewer struct {
	r        *Reviewer
	listener string
}

func (l laneReviewer) File(ctx context.Context, statement string) (analyzer.ReviewResult, error) {
	d, _ := analyzer.HoldDetailFrom(ctx)
	return l.r.file(l.listener, statement, d)
}

func (l laneReviewer) Claim(_ context.Context, id string) (analyzer.ReviewResult, error) {
	return l.r.claim(id)
}

func openKey(listener, statement string) string { return listener + "\x00" + statement }

func (r *Reviewer) file(listener, statement string, d analyzer.HoldDetail) (analyzer.ReviewResult, error) {
	if len(statement) > maxStatementBytes {
		return analyzer.ReviewResult{}, fmt.Errorf("the statement is %d bytes, more than the %d a review takes",
			len(statement), maxStatementBytes)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked()
	if id, ok := r.open[openKey(listener, statement)]; ok {
		rv := r.reviews[id]
		switch rv.Status {
		case statusPending:
			return analyzer.ReviewResult{ID: id, Status: statusPending}, nil
		case statusApproved:
			return r.spend(rv), nil
		}
	}
	if n := r.pendingLocked(); n >= maxPending {
		return analyzer.ReviewResult{}, fmt.Errorf("%d statements already wait for approval in this terminal", n)
	}
	id, err := newReviewID()
	if err != nil {
		return analyzer.ReviewResult{}, err
	}
	r.reviews[id] = &LocalReview{ID: id, Listener: listener, Statement: statement,
		Status: statusPending, Filed: r.now(),
		Risk: string(d.RiskLevel), Title: d.Title, Why: d.Explanation, Rule: d.Rule}
	r.open[openKey(listener, statement)] = id
	r.prune()
	r.poke()
	return analyzer.ReviewResult{ID: id, Status: statusPending}, nil
}

func (r *Reviewer) claim(id string) (analyzer.ReviewResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked()
	rv, ok := r.reviews[id]
	if !ok {
		return analyzer.ReviewResult{}, fmt.Errorf("no local approval %s", id)
	}
	if rv.Status == statusApproved {
		return r.spend(rv), nil
	}
	return analyzer.ReviewResult{ID: id, Status: rv.Status}, nil
}

// spend releases one statement on an approved review. Called with mu held.
func (r *Reviewer) spend(rv *LocalReview) analyzer.ReviewResult {
	rv.Status = statusExecuted
	delete(r.open, openKey(rv.Listener, rv.Statement))
	r.poke()
	return analyzer.ReviewResult{ID: rv.ID, Status: statusExecuted, Forward: true}
}

// Decide records the person's answer on a pending review. ok is false when
// the review is not pending any more, so a second key press changes nothing.
func (r *Reviewer) Decide(id string, approve bool, by string) (LocalReview, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked()
	rv, found := r.reviews[id]
	if !found {
		return LocalReview{}, false
	}
	if rv.Status != statusPending {
		return *rv, false
	}
	rv.Status = statusRejected
	if approve {
		rv.Status = statusApproved
	} else {
		delete(r.open, openKey(rv.Listener, rv.Statement))
	}
	rv.DecidedBy, rv.Decided = by, r.now()
	r.poke()
	return *rv, true
}

// expire settles the reviews past reviewLife. The TUI calls it on a timer,
// so an expired review leaves the screen without waiting for the next filing.
func (r *Reviewer) expire() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked()
}

// expireLocked does the work of expire. Called with mu held.
func (r *Reviewer) expireLocked() {
	now := r.now()
	changed := false
	for _, rv := range r.reviews {
		var since time.Time
		switch rv.Status {
		case statusPending:
			since = rv.Filed
		case statusApproved:
			since = rv.Decided
		default:
			continue
		}
		if now.Sub(since) < reviewLife {
			continue
		}
		rv.Status, rv.Expired = statusExpired, true
		delete(r.open, openKey(rv.Listener, rv.Statement))
		changed = true
	}
	if changed {
		r.prune()
		r.poke()
	}
}

func (r *Reviewer) pendingLocked() int {
	n := 0
	for _, rv := range r.reviews {
		if rv.Status == statusPending {
			n++
		}
	}
	return n
}

// poke raises the Changed flag without waiting.
func (r *Reviewer) poke() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// prune drops the oldest settled reviews past maxSettled. Called with mu.
func (r *Reviewer) prune() {
	var settled []*LocalReview
	for _, rv := range r.reviews {
		if rv.Status == statusRejected || rv.Status == statusExecuted || rv.Status == statusExpired {
			settled = append(settled, rv)
		}
	}
	if len(settled) <= maxSettled {
		return
	}
	sort.Slice(settled, func(i, j int) bool { return settled[i].Filed.Before(settled[j].Filed) })
	for _, rv := range settled[:len(settled)-maxSettled] {
		delete(r.reviews, rv.ID)
	}
}

func newReviewID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("review id: %w", err)
	}
	return "local-" + hex.EncodeToString(b), nil
}
