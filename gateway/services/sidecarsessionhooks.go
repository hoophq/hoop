package services

import (
	"errors"
	"slices"
	"sync"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/events"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/plugins/webhooks"
	"gorm.io/gorm"
)

// sidecarSessionHook is what an agent session fires on open and close.
// No Jira: a connect session files no issue.
type sidecarSessionHook struct {
	DB        *gorm.DB
	OrgID     string
	SessionID string
	// Opened is set on the batch that created the session.
	Opened bool
	// Closed is set on the batch, or the reap, that ended it.
	Closed bool
	Reaped bool
	// Republish re-runs the close events only. They are idempotent; the
	// webhook is not, so it is not sent again.
	Republish bool
}

const (
	// sidecarHookWorkers bounds the hooks running at once: a rollout opens
	// and closes thousands of sessions, and each hook reads the database.
	sidecarHookWorkers = 8
	// sidecarHookQueueSize bounds the hooks that wait for a worker.
	sidecarHookQueueSize = 1024
)

var (
	sidecarHooksOnce sync.Once
	sidecarHooks     *sidecarHookPool
)

// runSidecarSessionHooks fires in the background: a slow webhook endpoint
// must not hold the sidecar's batch.
func runSidecarSessionHooks(h sidecarSessionHook) {
	sidecarHooksOnce.Do(func() {
		sidecarHooks = newSidecarHookPool(sidecarHookWorkers, sidecarHookQueueSize, fireSidecarSessionHooks)
	})
	sidecarHooks.enqueue(h)
}

// sidecarHookPool runs hooks on a fixed number of workers.
type sidecarHookPool struct {
	queue chan sidecarSessionHook
}

func newSidecarHookPool(workers, size int, fire func(sidecarSessionHook)) *sidecarHookPool {
	p := &sidecarHookPool{queue: make(chan sidecarSessionHook, size)}
	for range workers {
		go func() {
			for h := range p.queue {
				fire(h)
			}
		}()
	}
	return p
}

// enqueue never blocks the batch: a full queue drops h, and it reports false.
func (p *sidecarHookPool) enqueue(h sidecarSessionHook) bool {
	select {
	case p.queue <- h:
		return true
	default:
		log.With("org", h.OrgID, "sid", h.SessionID).
			Warnf("the sidecar session hooks queue is full (%d), dropped the hooks of this session", cap(p.queue))
		return false
	}
}

// fireSidecarSessionHooks runs in order, so session.open goes before
// session.close when one batch holds both. Failures are logged: an agent
// session drops them the same way.
func fireSidecarSessionHooks(h sidecarSessionHook) {
	if !h.Opened && !h.Closed && !h.Republish {
		return
	}
	logger := log.With("sid", h.SessionID)
	// A worker goroutine: a panic here would stop the gateway.
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("sidecar session hooks panicked: %v", r)
		}
	}()
	sess, err := models.GetSessionByID(h.OrgID, h.SessionID)
	if err != nil {
		logger.Warnf("failed reading the sidecar session for its hooks, reason=%v", err)
		return
	}
	sendWebhooks := webhooksEnabled(h.DB, h.OrgID, sess.Connection)

	if h.Opened {
		conn, err := models.GetConnectionByOrgAndName(h.OrgID, sess.Connection)
		if err != nil && !errors.Is(err, models.ErrNotFound) {
			logger.Warnf("failed reading the sidecar session connection, reason=%v", err)
		}
		events.DeriveFromSessionStart(h.OrgID, sess, conn)
		if sendWebhooks {
			if err := webhooks.SendSidecarSessionOpen(sess); err != nil {
				logger.Warn(err)
			}
		}
	}
	if h.Republish && !h.Closed {
		events.DeriveFromSessionEnd(h.OrgID, sess)
	}
	if h.Closed {
		events.DeriveFromSessionEnd(h.OrgID, sess)
		if sendWebhooks {
			if err := webhooks.SendSidecarSessionClose(h.OrgID, h.SessionID, h.Reaped); err != nil {
				logger.Warn(err)
			}
		}
	}
}

// webhooksEnabled reports the webhooks plugin on for the connection, the
// gate an agent session passes (streamclient.loadRuntimePlugins).
func webhooksEnabled(db *gorm.DB, orgID, connection string) bool {
	p, err := models.GetPluginByName(db, orgID, plugintypes.PluginWebhookName)
	if err != nil {
		if !errors.Is(err, models.ErrNotFound) {
			log.With("org", orgID).Warnf("failed reading the webhooks plugin, reason=%v", err)
		}
		return false
	}
	return slices.ContainsFunc(p.Connections, func(c *models.PluginConnection) bool {
		return c.ConnectionName == connection
	})
}
