package models

import "time"

// SessionListFilterSQL returns the FROM and WHERE the session list builds for
// opt, so a test with a database can read the plan of the real SQL.
func SessionListFilterSQL(isAuditorOrAdmin bool, opt SessionOption) (from, where string) {
	sh := newSessionListShape(isAuditorOrAdmin, opt)
	return sh.from(), sh.where()
}

const (
	SessionByIDQuery       = sessionByIDQuery
	SessionBlobInputQuery  = sessionBlobInputQuery
	SessionBlobStreamQuery = sessionBlobStreamQuery
)

const LastUsedInterval = lastUsedInterval

// ResetLastUsedGates makes the next call behave as a call on another replica.
func ResetLastUsedGates() {
	apiKeyLastUsedGate = lastUsedGate{}
	aiAgentLastUsedGate = lastUsedGate{}
}

func LastUsedGateAllow(id string, now time.Time) bool {
	return apiKeyLastUsedGate.allow(id, now)
}
