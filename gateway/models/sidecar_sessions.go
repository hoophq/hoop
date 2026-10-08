package models

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/proto"
	"gorm.io/gorm"
)

// SidecarSessionState is what the next batch of a sidecar session reads.
type SidecarSessionState struct {
	CreatedAt time.Time
	Metrics   map[string]any
	// LastSeq is the highest seq applied; zero when none.
	LastSeq int64
	// Principal is the user the row is filed under, email first.
	Principal string
	// GuardRails counts guardrails_info entries; GuardRailsOmitted, those the
	// cap left out.
	GuardRails        int
	GuardRailsOmitted int64
	// Done is true once the session ended.
	Done bool
	// Reaped is true when the reaper ended the session, not a session_end.
	Reaped bool
	// ReviewSessions are the sessions of the reviews this session links to.
	ReviewSessions []string
}

// LockSidecarSession serializes a session's writers until tx ends. A row lock
// is not enough: two resends can both create the row.
func LockSidecarSession(tx *gorm.DB, sessionID string) error {
	return tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
		"sidecar-session:"+sessionID).Error
}

// GetSidecarSessionState returns gorm.ErrRecordNotFound when the session does
// not exist yet.
func GetSidecarSessionState(tx *gorm.DB, orgID, sessionID string) (*SidecarSessionState, error) {
	var row struct {
		CreatedAt         time.Time
		Metrics           []byte
		LastSeq           *int64
		Principal         string
		GuardRails        int
		GuardRailsOmitted *int64
		Done              bool
		Reaped            bool
		ReviewSessions    []byte
	}
	err := tx.Raw(`
	SELECT created_at, metrics, status = 'done' AS done,
		(metadata->'sidecar'->>'last_seq')::BIGINT AS last_seq,
		COALESCE(NULLIF(user_email, ''), user_name, '') AS principal,
		CASE WHEN jsonb_typeof(guardrails_info) = 'array'
			THEN jsonb_array_length(guardrails_info) ELSE 0 END AS guard_rails,
		(metadata->'sidecar'->>'guardrails_omitted')::BIGINT AS guard_rails_omitted,
		metadata->'sidecar'->>'reaped_at' IS NOT NULL AS reaped,
		metadata->'sidecar'->'review_sessions' AS review_sessions
	FROM private.sessions
	WHERE org_id = ? AND id = ?`, orgID, sessionID).
		Take(&row).Error
	if err != nil {
		return nil, err
	}
	state := &SidecarSessionState{CreatedAt: row.CreatedAt, Principal: row.Principal,
		GuardRails: row.GuardRails, Done: row.Done, Reaped: row.Reaped}
	if len(row.ReviewSessions) > 0 {
		if err := json.Unmarshal(row.ReviewSessions, &state.ReviewSessions); err != nil {
			return nil, fmt.Errorf("failed decoding review sessions: %w", err)
		}
	}
	if row.GuardRailsOmitted != nil {
		state.GuardRailsOmitted = *row.GuardRailsOmitted
	}
	if row.LastSeq != nil {
		state.LastSeq = *row.LastSeq
	}
	if len(row.Metrics) > 0 {
		if err := json.Unmarshal(row.Metrics, &state.Metrics); err != nil {
			return nil, fmt.Errorf("failed decoding session metrics: %w", err)
		}
	}
	return state, nil
}

// SetSidecarSessionProgress merges sidecar into metadata.sidecar, and replaces
// metrics when it is not nil.
func SetSidecarSessionProgress(tx *gorm.DB, orgID, sessionID string, sidecar map[string]any, metrics map[string]any) error {
	sidecarJSON, err := json.Marshal(sidecar)
	if err != nil {
		return fmt.Errorf("failed encoding sidecar metadata: %w", err)
	}
	updates := map[string]any{
		"metadata": gorm.Expr(`jsonb_set(COALESCE(metadata, '{}'::jsonb), '{sidecar}',
			COALESCE(metadata->'sidecar', '{}'::jsonb) || ?::jsonb, true)`, string(sidecarJSON)),
	}
	if metrics != nil {
		metricsJSON, err := json.Marshal(metrics)
		if err != nil {
			return fmt.Errorf("failed encoding session metrics: %w", err)
		}
		updates["metrics"] = gorm.Expr(`?::jsonb`, string(metricsJSON))
	}
	res := tx.Table("private.sessions").
		Where("org_id = ? AND id = ?", orgID, sessionID).
		Updates(updates)
	if res.Error == nil && res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return res.Error
}

// SetSidecarSessionUser files the session under a principal resolved late.
func SetSidecarSessionUser(tx *gorm.DB, orgID, sessionID, userName, userEmail string) error {
	res := tx.Table("private.sessions").
		Where("org_id = ? AND id = ?", orgID, sessionID).
		Updates(map[string]any{"user_name": userName, "user_email": userEmail})
	if res.Error == nil && res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return res.Error
}

// ListSidecarMirrorNames maps each listener of a sidecar to the name of its
// mirror connection. A mirror can carry its fallback name, so a session files
// its connection from here, never by rebuilding the name.
func ListSidecarMirrorNames(db *gorm.DB, orgID, sidecarID string) (map[string]string, error) {
	var rows []struct {
		Name     string
		Listener string `gorm:"column:sidecar_listener"`
	}
	err := db.Raw(`SELECT name, sidecar_listener FROM private.connections
		WHERE org_id = ? AND sidecar_id = ? AND sidecar_listener IS NOT NULL`, orgID, sidecarID).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Listener] = r.Name
	}
	return out, nil
}

// LinkSidecarReviewsToSession records sessionID as metadata.sidecar_session_id
// on the session each review waits in, and returns those sessions. Only the
// reviews sidecarID filed are touched: a review id in an event is not trusted.
func LinkSidecarReviewsToSession(tx *gorm.DB, orgID, sidecarID, sessionID string, reviewIDs []string) ([]string, error) {
	if len(reviewIDs) == 0 {
		return nil, nil
	}
	var linked []string
	err := tx.Raw(`
	UPDATE private.sessions s
	SET metadata = jsonb_set(COALESCE(s.metadata, '{}'::jsonb), '{sidecar_session_id}', to_jsonb(?::text), true)
	FROM private.reviews r
	WHERE r.org_id = ? AND r.sidecar_id = ? AND r.id IN ?
		AND s.org_id = r.org_id AND s.id = r.session_id
	RETURNING s.id`, sessionID, orgID, sidecarID, reviewIDs).
		Scan(&linked).Error
	return linked, err
}

// StaleSidecarSession is an open sidecar session the reaper may end.
type StaleSidecarSession struct {
	ID    string `gorm:"column:id"`
	OrgID string `gorm:"column:org_id"`
}

// SidecarSessionStaleness is when an open sidecar session counts as dead.
type SidecarSessionStaleness struct {
	// SidecarSeenBefore: its sidecar has not heartbeat since, or is gone.
	SidecarSeenBefore time.Time
	// EventBefore: it received no event since, while its sidecar runs on.
	EventBefore time.Time
}

// staleSidecarSession is the condition both queries apply to the row s.
// created_at is TIMESTAMP holding UTC; AT TIME ZONE 'UTC' reads it as the
// instant it is, whatever the server's TimeZone.
const staleSidecarSession = `s.origin = @origin AND s.status = 'open' AND (
	NOT EXISTS (
		SELECT 1 FROM private.sidecars sc
		WHERE sc.org_id = s.org_id AND sc.id::text = s.metadata->'sidecar'->>'id'
			AND COALESCE(sc.last_seen_at, s.created_at AT TIME ZONE 'UTC') >= @seen)
	OR COALESCE((s.metadata->'sidecar'->>'last_event_at')::timestamptz,
		s.created_at AT TIME ZONE 'UTC') < @event)`

func (st SidecarSessionStaleness) args() map[string]any {
	return map[string]any{"origin": proto.SessionOriginSidecar, "seen": st.SidecarSeenBefore, "event": st.EventBefore}
}

// ListStaleSidecarSessions returns up to limit open sidecar sessions st
// counts as dead, oldest first.
func ListStaleSidecarSessions(ctx context.Context, db *gorm.DB, st SidecarSessionStaleness, limit int) ([]StaleSidecarSession, error) {
	args := st.args()
	args["limit"] = limit
	var out []StaleSidecarSession
	err := db.WithContext(ctx).Raw(`SELECT s.id, s.org_id FROM private.sessions s
	WHERE `+staleSidecarSession+`
	ORDER BY s.created_at
	LIMIT @limit`, args).Scan(&out).Error
	return out, err
}

// ReapSidecarSession ends a session st still counts as dead, at its last
// event, and marks it reaped. It reports false when a batch revived or ended
// it since it was listed. The caller holds LockSidecarSession.
func ReapSidecarSession(tx *gorm.DB, orgID, sessionID string, st SidecarSessionStaleness, now time.Time) (bool, error) {
	args := st.args()
	args["org"], args["id"], args["now"] = orgID, sessionID, now
	res := tx.Exec(`UPDATE private.sessions s
	SET status = 'done',
		ended_at = GREATEST(s.created_at,
			((s.metadata->'sidecar'->>'last_event_at')::timestamptz AT TIME ZONE 'UTC')),
		metadata = jsonb_set(COALESCE(s.metadata, '{}'::jsonb), '{sidecar}',
			COALESCE(s.metadata->'sidecar', '{}'::jsonb) || jsonb_build_object('reaped_at', CAST(@now AS timestamptz)), true)
	WHERE s.org_id = @org AND s.id = @id AND `+staleSidecarSession, args)
	return res.RowsAffected == 1, res.Error
}
