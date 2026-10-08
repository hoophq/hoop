package webhooks

import (
	"github.com/hoophq/hoop/gateway/models"
)

// ExitCodeUnknown is the session.close exit code of a session whose client
// reported none.
const ExitCodeUnknown = -100

// reapedExitErr is the exit_err of a sidecar session the gateway ended.
const reapedExitErr = "the gateway ended the session: its sidecar stopped sending events"

// SendSidecarSessionOpen sends session.open for a sidecar session, with the
// agent payload's keys: the schema refuses any other.
func SendSidecarSessionOpen(sess *models.Session) error {
	return SendMessage(sess.OrgID, eventSessionOpenType, map[string]any{
		"event_type":         eventSessionOpenType,
		"id":                 sess.ID,
		"user_id":            sess.UserID,
		"user_email":         sess.UserEmail,
		"connection":         sess.Connection,
		"connection_type":    sess.ConnectionType,
		"connection_envs":    []string{},
		"input":              []byte{},
		"is_input_truncated": false,
		"input_size":         0,
		"input_envs":         []string{},
		"has_input_args":     false,
		"review":             nil,
		"command":            []string{},
		"verb":               sess.Verb,
	})
}

// SendSidecarSessionClose sends session.close for a sidecar session. A
// sidecar sends no exit code: an ended session is 0, a reaped one unknown.
func SendSidecarSessionClose(orgID, sessionID string, reaped bool) error {
	exitCode, exitErr := 0, (*string)(nil)
	if reaped {
		msg := reapedExitErr
		exitCode, exitErr = ExitCodeUnknown, &msg
	}
	return SendMessage(orgID, eventSessionCloseType, map[string]any{
		"event_type": eventSessionCloseType,
		"id":         sessionID,
		"exit_code":  exitCode,
		"exit_err":   exitErr,
	})
}
