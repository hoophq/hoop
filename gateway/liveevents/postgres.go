package liveevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	notifyChannel = "hoop_liveevents"
	// Postgres refuses a NOTIFY payload of 8000 bytes or more.
	maxPayload    = 7999
	outboxSize    = 256
	notifyTimeout = 5 * time.Second
)

// envelope is one NOTIFY payload. A probe carries no event.
type envelope struct {
	Replica string `json:"replica"`
	OrgID   string `json:"org,omitempty"`
	Event   *Event `json:"event,omitempty"`
	Probe   string `json:"probe,omitempty"`
}

type transport interface {
	notify(ctx context.Context, payload string) error
	// listen calls ready once it listens, then onPayload for each
	// notification, until ctx ends or the session fails.
	listen(ctx context.Context, ready func(), onPayload func(string)) error
}

type timings struct {
	probeEvery, probeWait, minBackoff, maxBackoff time.Duration
}

var defaultTimings = timings{
	probeEvery: 30 * time.Second,
	probeWait:  5 * time.Second,
	minBackoff: time.Second,
	maxBackoff: time.Minute,
}

// Run joins the default hub to the other replicas until ctx ends. dsn opens
// the LISTEN connection; db sends NOTIFY.
func Run(ctx context.Context, dsn string, db *sql.DB) { defaultHub.Run(ctx, dsn, db) }

func (h *Hub) Run(ctx context.Context, dsn string, db *sql.DB) {
	h.run(ctx, pgTransport{dsn: dsn, db: db}, defaultTimings)
}

func (h *Hub) run(ctx context.Context, t transport, tm timings) {
	outbox := make(chan envelope, outboxSize)
	probes := make(chan string, 1)
	h.mu.Lock()
	h.outbox = outbox
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.outbox = nil
		h.mu.Unlock()
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); h.send(ctx, t, outbox) }()
	go func() { defer wg.Done(); h.probe(ctx, t, probes, tm) }()
	h.listen(ctx, t, probes, tm)
	wg.Wait()
}

func (h *Hub) send(ctx context.Context, t transport, outbox <-chan envelope) {
	for {
		select {
		case <-ctx.Done():
			return
		case env := <-outbox:
			payload, _ := json.Marshal(env)
			if len(payload) > maxPayload {
				h.log.Errorf("live events: a %v event of %d bytes is over the NOTIFY limit, other replicas miss it",
					env.Event.Type, len(payload))
				continue
			}
			nctx, cancel := context.WithTimeout(ctx, notifyTimeout)
			err := t.notify(nctx, string(payload))
			cancel()
			if err != nil {
				h.log.Warnf("live events: failed sending a %v event to other replicas, reason=%v", env.Event.Type, err)
				continue
			}
			h.dropping.Store(false)
		}
	}
}

func (h *Hub) listen(ctx context.Context, t transport, probes chan<- string, tm timings) {
	wait := tm.minBackoff
	for {
		err := t.listen(ctx,
			func() { wait = tm.minBackoff },
			func(payload string) { h.receive(payload, probes) })
		if ctx.Err() != nil {
			return
		}
		h.log.Warnf("live events: the LISTEN connection failed, retrying in %v, reason=%v", wait, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, tm.maxBackoff)
	}
}

func (h *Hub) receive(payload string, probes chan<- string) {
	var env envelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		h.log.Warnf("live events: dropped a NOTIFY payload it cannot read, reason=%v", err)
		return
	}
	switch {
	case env.Replica != h.replica:
		if env.Event != nil {
			h.deliver(env.OrgID, *env.Event)
		}
	case env.Probe != "":
		select {
		case probes <- env.Probe:
		default:
		}
	}
}

func (h *Hub) probe(ctx context.Context, t transport, probes <-chan string, tm timings) {
	healthy := true
	tick := time.NewTicker(tm.probeEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		ok := h.probeOnce(ctx, t, probes, tm.probeWait)
		if ctx.Err() != nil {
			return
		}
		switch {
		case ok && !healthy:
			h.log.Infof("live events: NOTIFY works again, events reach every replica")
		case !ok && healthy:
			h.log.Warnf("live events: a NOTIFY did not come back in %v, events reach only the browsers on this replica. "+
				"Check for a connection pooler in transaction mode, such as PgBouncer", tm.probeWait)
		}
		healthy = ok
	}
}

func (h *Hub) probeOnce(ctx context.Context, t transport, probes <-chan string, wait time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	nonce := uuid.NewString()
	payload, _ := json.Marshal(envelope{Replica: h.replica, Probe: nonce})
	if err := t.notify(ctx, string(payload)); err != nil {
		return false
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case got := <-probes:
			if got == nonce {
				return true
			}
		}
	}
}

type pgTransport struct {
	dsn string
	db  *sql.DB
}

func (p pgTransport) notify(ctx context.Context, payload string) error {
	_, err := p.db.ExecContext(ctx, "SELECT pg_notify($1, $2)", notifyChannel, payload)
	return err
}

func (p pgTransport) listen(ctx context.Context, ready func(), onPayload func(string)) error {
	conn, err := pgx.Connect(ctx, p.dsn)
	if err != nil {
		return err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+notifyChannel); err != nil {
		return err
	}
	ready()
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		onPayload(n.Payload)
	}
}
