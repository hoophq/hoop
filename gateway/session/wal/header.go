package wal

import (
	"fmt"
	"time"
)

type Header struct {
	EventLogVersion string     `json:"log_version"`
	OrgID           string     `json:"org_id"`
	SessionID       string     `json:"session_id"`
	Status          string     `json:"status"`
	StartDate       *time.Time `json:"start_date"`
}

func (h *Header) Validate() error {
	if h.OrgID == "" || h.SessionID == "" {
		return fmt.Errorf(`missing required values for wal session`)
	}
	return nil
}
