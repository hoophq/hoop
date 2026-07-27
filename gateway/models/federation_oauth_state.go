package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// FederationOAuthState is a short-lived row backing the gcp_oauth consent
// flow. The authorize endpoint creates it keyed by a random UUID (the OAuth
// "state" parameter); the callback endpoint consumes it to recover which
// (org, connection, user) initiated the flow and where to redirect the
// browser afterwards. Rows are deleted on use; the callback also rejects rows
// older than a fixed TTL to bound replay of a leaked state value.
type FederationOAuthState struct {
	ID           string    `gorm:"column:id"`
	OrgID        string    `gorm:"column:org_id"`
	ConnectionID string    `gorm:"column:connection_id"`
	UserID       string    `gorm:"column:user_id"`
	UserEmail    string    `gorm:"column:user_email"`
	RedirectURL  string    `gorm:"column:redirect_url"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

const federationOAuthStatesTable = "private.federation_oauth_states"

// CreateFederationOAuthState persists a new consent-flow state row.
func CreateFederationOAuthState(db *gorm.DB, state *FederationOAuthState) error {
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now().UTC()
	}
	return db.Table(federationOAuthStatesTable).Create(state).Error
}

// GetFederationOAuthState retrieves a consent-flow state row by its UUID.
// Returns ErrNotFound when the state is unknown (typo, already consumed, or
// forged).
func GetFederationOAuthState(db *gorm.DB, id string) (*FederationOAuthState, error) {
	var resp FederationOAuthState
	err := db.Table(federationOAuthStatesTable).
		Where("id = ?", id).
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteFederationOAuthState removes a consent-flow state row. Called once the
// callback has consumed it (success or failure) so a state value is single
// use.
func DeleteFederationOAuthState(db *gorm.DB, id string) error {
	return db.Exec(`DELETE FROM private.federation_oauth_states WHERE id = ?`, id).Error
}
