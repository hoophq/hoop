package models

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// SidecarSessionState is what a sidecar session row holds that the next
// batch of its events depends on.
type SidecarSessionState struct {
	CreatedAt time.Time
	Metrics   map[string]any
	// LastSeq is the highest event seq applied to the session. Zero when the
	// row predates it or holds none.
	LastSeq int64
	// Principal is the user the row is filed under, email first.
	Principal string
	// GuardRails is how many guardrails_info entries the row holds, and
	// GuardRailsOmitted how many more a cap left out.
	GuardRails        int
	GuardRailsOmitted int64
}

// LockSidecarSession serializes the writers of one session until tx ends.
//
// A sidecar resends a batch the gateway took too long to answer, so two
// requests can carry the same session at once. The row lock is not enough:
// before the first batch there is no row to lock, and both would create it.
func LockSidecarSession(tx *gorm.DB, sessionID string) error {
	return tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
		"sidecar-session:"+sessionID).Error
}

// GetSidecarSessionState reads the state of one session. It returns
// gorm.ErrRecordNotFound when the session does not exist yet.
func GetSidecarSessionState(tx *gorm.DB, orgID, sessionID string) (*SidecarSessionState, error) {
	var row struct {
		CreatedAt         time.Time
		Metrics           []byte
		LastSeq           *int64
		Principal         string
		GuardRails        int
		GuardRailsOmitted *int64
	}
	err := tx.Raw(`
	SELECT created_at, metrics, (metadata->'sidecar'->>'last_seq')::BIGINT AS last_seq,
		COALESCE(NULLIF(user_email, ''), user_name, '') AS principal,
		CASE WHEN jsonb_typeof(guardrails_info) = 'array'
			THEN jsonb_array_length(guardrails_info) ELSE 0 END AS guard_rails,
		(metadata->'sidecar'->>'guardrails_omitted')::BIGINT AS guard_rails_omitted
	FROM private.sessions
	WHERE org_id = ? AND id = ?`, orgID, sessionID).
		Take(&row).Error
	if err != nil {
		return nil, err
	}
	state := &SidecarSessionState{CreatedAt: row.CreatedAt, Principal: row.Principal, GuardRails: row.GuardRails}
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

// SetSidecarSessionProgress records what a batch applied: sidecar is merged
// into metadata.sidecar (last_seq always rides in it), and metrics replaces
// the metrics column when it is not nil.
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

// SetSidecarSessionUser files the session under the principal the sidecar
// resolved after the session started.
func SetSidecarSessionUser(tx *gorm.DB, orgID, sessionID, userName, userEmail string) error {
	res := tx.Table("private.sessions").
		Where("org_id = ? AND id = ?", orgID, sessionID).
		Updates(map[string]any{"user_name": userName, "user_email": userEmail})
	if res.Error == nil && res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return res.Error
}
