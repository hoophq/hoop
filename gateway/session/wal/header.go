package wal

import (
	"fmt"
	"time"
)

type Header struct {
	OrgID          string     `json:"org_id"`
	SessionID      string     `json:"session_id"`
	UserID         string     `json:"user_id"`
	UserName       string     `json:"user_name"`
	UserEmail      string     `json:"user_email"`
	ConnectionName string     `json:"connection_name"`
	ConnectionType string     `json:"connection_type"`
	Status         string     `json:"status"`
	Script         string     `json:"script"`
	Labels         string     `json:"labels"` // we save it as string and convert at storage layer
	Verb           string     `json:"verb"`
	StartDate      *time.Time `json:"start_date"`
	// SessionToken is a token that is used
	// to persist the contents to a remote source
	SessionToken string `json:"session_token"`
}

func (h *Header) Validate() error {
	if h.OrgID == "" || h.SessionID == "" ||
		h.ConnectionType == "" || h.ConnectionName == "" ||
		h.StartDate == nil {
		return fmt.Errorf(`missing required values for wal session`)
	}
	return nil
}
