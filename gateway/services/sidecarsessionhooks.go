package services

import (
	"errors"
	"slices"

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

// runSidecarSessionHooks fires in the background. It waits for a hook slot
// first, so a database backlog holds the batch's ingest slot and new batches
// get a 503. The webhooks go out after the slot is free: a slow webhook
// endpoint must not hold the ingest. Their own timeout bounds them.
func runSidecarSessionHooks(h sidecarSessionHook) {
	runSidecarSessionHooksWith(h, fireSidecarSessionHooks)
}

func runSidecarSessionHooksWith(h sidecarSessionHook, fire func(sidecarSessionHook) func()) {
	slots := sidecarSlots.Load().hooks
	slots <- struct{}{}
	go func() {
		send := fire(h)
		<-slots
		if send != nil {
			send()
		}
	}()
}

// fireSidecarSessionHooks derives the session events and returns the webhook
// sends, in order, so session.open goes before session.close when one batch
// holds both. Failures are logged: an agent session drops them the same way.
func fireSidecarSessionHooks(h sidecarSessionHook) (send func()) {
	if !h.Opened && !h.Closed && !h.Republish {
		return nil
	}
	logger := log.With("sid", h.SessionID)
	// A bare goroutine: a panic here would stop the gateway.
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("sidecar session hooks panicked: %v", r)
			send = nil
		}
	}()
	sess, err := models.GetSessionByID(h.OrgID, h.SessionID)
	if err != nil {
		logger.Warnf("failed reading the sidecar session for its hooks, reason=%v", err)
		return nil
	}
	sendWebhooks := webhooksEnabled(h.DB, h.OrgID, sess.Connection)

	if h.Opened {
		conn, err := models.GetConnectionByOrgAndName(h.OrgID, sess.Connection)
		if err != nil && !errors.Is(err, models.ErrNotFound) {
			logger.Warnf("failed reading the sidecar session connection, reason=%v", err)
		}
		events.DeriveFromSessionStart(h.OrgID, sess, conn)
	}
	if h.Republish || h.Closed {
		events.DeriveFromSessionEnd(h.OrgID, sess)
	}
	if !sendWebhooks || (!h.Opened && !h.Closed) {
		return nil
	}
	return func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("sidecar session webhooks panicked: %v", r)
			}
		}()
		if h.Opened {
			if err := webhooks.SendSidecarSessionOpen(sess); err != nil {
				logger.Warn(err)
			}
		}
		if h.Closed {
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
