# The decision an SSH lane defers to.
#
# What makes this worth reading is WHERE input.context comes from. The lane
# verified the client's certificate itself, at the handshake, so `subject`
# below is the key id a CA signed — not a header somebody set, and not a name
# a component upstream claimed. The same policy shape serves a Postgres lane;
# the identity just arrives from a different place.
#
# Two decisions live here, and they are deliberately different in kind:
#
#   no-network-tools   acts on a FINDING. The lane's deny_words_list rule
#                      matched locally, for free, and deferred the verdict.
#                      Rego decides what the match MEANS for this principal.
#   read-only-lane     acts on the OPERATION alone. No local rule exists for
#                      it, and none is needed: `sftp_write` is a fact the
#                      protocol reports.
package hoop.ssh

import rego.v1

# A single-call lane sends no phase at all. Reading an absent phase as
# "decide" keeps this policy answering there; without it `decision` is
# undefined and fail_open: false denies every statement.
phase := object.get(input, "phase", "decide")

# What the deferred deny_words_list rule established. Empty when nothing
# matched, which is a different fact from "the scanner never ran".
flagged := object.get(input, ["findings", "deny_words_list", "values", "words"], [])

words_answered if input.findings.deny_words_list.status in {"ok", "cached"}

# The one identity allowed to run a flagged command. It is a CERTIFICATE key
# id: to become this principal you need the CA to sign you as it, which is
# the whole point of putting the check here rather than in a word list.
break_glass := "incident-response@example.com"

# Everything an sftp session may not do on a read-only lane.
writes := {
	"sftp_write", "sftp_remove", "sftp_rename", "sftp_mkdir",
	"sftp_rmdir", "sftp_setstat", "sftp_symlink",
}

decision := {"denied": true, "rule": "words-unreadable", "message": msg} if {
	# Status before values, always. "found nothing" and "never ran" are
	# different answers and only one of them is safe to allow.
	phase == "decide"
	input.findings.deny_words_list
	not words_answered
	msg := sprintf("the word scanner reported %v; refusing", [input.findings.deny_words_list.status])
} else := {"allow": true, "rule": "break-glass"} if {
	phase == "decide"
	count(flagged) > 0
	input.context.subject == break_glass
} else := {"denied": true, "rule": "no-network-tools", "message": msg} if {
	phase == "decide"
	count(flagged) > 0
	msg := sprintf(
		"%v is not available to %v on this host; an incident-response certificate may run it",
		[concat(", ", sort(flagged)), input.context.subject],
	)
} else := {"denied": true, "rule": "read-only-lane", "message": "this lane may read files and not change them"} if {
	phase == "decide"
	input.operation in writes
} else := {"allow": true}
