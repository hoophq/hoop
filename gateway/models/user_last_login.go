package models

import "gorm.io/gorm"

// The last_login_at column is deliberately absent from the User struct, for the
// reason user_signup_origin.go gives: UpdateUser writes every mapped column
// through DB.Save, so a partial User literal would reset it. RecordUserLogin is
// the only write path and ListUsersWithLastLogin the only read path.

// RecordUserLogin stores the current time as the last login of the user with
// this subject. Only an active user is updated, so a sign-in that is refused
// for an inactive or invited user leaves no trace. Call it after the subject is
// final: an OIDC sign-in rewrites the subject of an invited user.
func RecordUserLogin(db *gorm.DB, subject string) error {
	return db.Exec(`
		UPDATE private.users SET last_login_at = NOW()
		WHERE subject = ? AND status = 'active'`, subject).Error
}
