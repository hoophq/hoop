package models

// SessionListFilterSQL returns the FROM and WHERE the session list builds for
// opt, so a test with a database can read the plan of the real SQL.
func SessionListFilterSQL(isAuditorOrAdmin bool, opt SessionOption) (from, where string) {
	sh := newSessionListShape(isAuditorOrAdmin, opt)
	return sh.from(), sh.where()
}
