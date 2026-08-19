package review

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/hoophq/hoopinspect"
)

// markerPrefix is the ONE accepted spelling of the hoop correlation marker in
// a SQL statement.
//
// It is hoopinspect.CorrelationHeader lowercased, so the same name identifies
// a unit of work whichever protocol carries it: a header on HTTP, this comment
// on SQL. A test asserts the two stay in step, because the whole value of one
// name is lost the moment they drift.
//
// Rigid on purpose. "Strip the marker" has to mean exactly one thing on both
// the create path and the claim path, or the same logical statement leaves
// different residue in each and an approval becomes unmatchable. Leading
// position, single occurrence, fixed syntax; anything that does not conform is
// left in place rather than hunted for.
//
// Case-SENSITIVE, unlike the HTTP header, and that asymmetry is the protocols'
// rather than a choice: net/http canonicalizes header names, while a SQL
// comment is bytes in a statement, and matching it case-insensitively would
// mean two spellings of the marker producing two different residues — exactly
// the unmatchable-approval failure the rigidity above exists to prevent.
const markerPrefix = "-- x-hoop-correlation-id="

// MaxMarkerLen bounds the marker value.
//
// The marker is agent-supplied and reaches a database column, so it is length
// checked here rather than trusted. A value that overruns is not a conforming
// marker, which means it is left in the statement and lands in the hash — the
// failure is a review that never matches, never one that matches too much.
const MaxMarkerLen = 128

// canonicalize splits a statement's verbatim text into the text that gets
// hashed and the marker hoop injected, and is the ONLY place either is
// derived.
//
// One function, two callers (the claim path and the create path). Drift
// between those two is the failure that makes approvals silently unmatchable,
// so there is deliberately no second implementation to drift from.
//
// It removes only what hoop itself injected:
//
//  1. strip the marker, in the one strict anchored form above;
//  2. trim leading and trailing whitespace of the whole statement;
//  3. stop.
//
// No case folding, no interior whitespace collapsing, no comment stripping
// beyond hoop's own marker. Each of those is a claim about SQL semantics, and
// the two ways of being wrong are not symmetric: over-normalizing collides two
// different statements, so an approval for one authorizes the other — a silent
// bypass. Under-normalizing makes a retry hash differently, so approvals
// visibly stop working on the first test. Concretely, `'Alice'` and `'alice'`
// select different rows and `"Customers"` and `customers` are different
// relations, `note = 'a  b'` is not `note = 'a b'`, and `/*+ ... */` hints and
// MySQL's `/*! ... */` change behavior. The safe rule is byte equality after
// removing the one thing hoop added.
func canonicalize(text string) (canonical, marker string) {
	rest, marker, ok := cutMarker(text)
	if !ok {
		// Non-conforming: the text is hashed exactly as it arrived, marker
		// and all. Both paths do the same, so the two hashes still agree.
		return strings.TrimSpace(text), ""
	}
	return strings.TrimSpace(rest), marker
}

// cutMarker removes a conforming marker line from the front of text.
//
// ok=false means there is no conforming marker, and the caller MUST leave the
// text untouched: a marker that is almost right is content, not metadata.
func cutMarker(text string) (rest, marker string, ok bool) {
	if !strings.HasPrefix(text, markerPrefix) {
		return text, "", false
	}
	line := text[len(markerPrefix):]
	// The marker occupies exactly one line. A statement that is nothing but
	// a marker (no newline) still yields one, and leaves empty text behind
	// for the caller to reject.
	value, remainder, hasNewline := strings.Cut(line, "\n")
	value = strings.TrimRight(value, " \t\r")
	if !validMarker(value) {
		return text, "", false
	}
	if !hasNewline {
		remainder = ""
	}
	return remainder, value, true
}

// validMarker reports whether v is an acceptable marker value.
//
// The charset is an allowlist rather than a denylist because the value is
// agent-supplied and is used as a lookup key: a marker carrying a newline or a
// NUL would make the audit line and the query predicate disagree about where
// it ends.
func validMarker(v string) bool {
	if v == "" || len(v) > MaxMarkerLen {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-' || c == ':' || c == '@' || c == '/' || c == '+':
		default:
			return false
		}
	}
	return true
}

// headerMarker returns v when it is an acceptable marker, and "" otherwise.
//
// Unlike the SQL path there is no "leave it in place" option — a header is
// metadata or it is nothing — so a value that is too long or carries a
// character outside the allowlist yields no marker at all. That degrades to
// the session marker, or to no marker, and a lane with require_marker on then
// refuses the statement outright. Both are visible; neither is a wrong match.
func headerMarker(v string) string {
	if !validMarker(v) {
		return ""
	}
	return v
}

// markerHowTo says how to supply a correlation marker on this protocol.
//
// It exists because "prefix it with a -- comment" is advice a caller on an
// HTTP lane cannot act on, and a refusal that tells someone to do the
// impossible reads as a broken gate rather than a configured one.
//
// Kept adjacent to canonicalFor, and covered by a test that walks every
// protocol canonicalFor accepts: a new codec that can be gated but has no
// advice here fails that test rather than shipping SQL instructions to a
// protocol with no comment syntax.
func markerHowTo(protocol hoopinspect.Protocol) string {
	switch protocol {
	case hoopinspect.HTTP:
		return "send it in the " + hoopinspect.CorrelationHeader + " request header"
	case hoopinspect.Postgres, hoopinspect.MSSQL:
		return "prefix the statement with \"" + markerPrefix + "<id>\" on its own line"
	default:
		return "this protocol has no supported way to carry one"
	}
}

// execKey is the AUTHORIZATION key: SHA-256 over the canonical statement text.
//
// It is EXACT, and that is the whole point of it.
//
// Do not confuse it with analyzer.sqlCacheKey, which hashes the statement
// SHAPE with literals stripped so `WHERE id = 1` and `WHERE id = 2` collapse
// onto one entry. That is safe for a cost cache — the cache never turns a
// block into an allow, it only reuses a verdict for an identical shape — and
// it is a review bypass here: approve `DELETE FROM users WHERE id = 1` and a
// shape-keyed lookup would authorize `DELETE FROM users WHERE id = 999`.
//
// The two functions live in different packages so nobody reaches for the wrong
// one by autocomplete. Never swap them.
//
// Org, sandbox and connection are deliberately NOT hashed in. They are query
// predicates the gateway applies from the credential it already authenticated,
// which puts the scoping on the side of the trust boundary that owns it and
// keeps a non-match debuggable.
func execKey(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// statementIdentity is everything the gate derives from one statement before
// it talks to the gateway.
type statementIdentity struct {
	// Canonical is the text a human is asked to approve and the text Hash
	// covers. It is sent on the create path so the reviewer reads exactly
	// what an approval will authorize.
	Canonical string

	// Hash is the authorization key. Server-computed in the sense that
	// matters: it comes from the bytes on the wire, never from a field the
	// agent chose.
	Hash string

	// Marker is the REQUEST identity — "is this a new request, or a retry of
	// one already filed". Agent-supplied, and that is fine: it decides how
	// many reviews reach the queue, never what an approval permits. The
	// authorization path never sees it.
	Marker string

	// Observable is false when the gate cannot see everything that will
	// execute, which is a refusal rather than a gate. Why says which case.
	Observable bool
	Why        string
}

// identify derives the two keys for one statement.
//
// sessionMarker is the connection-level correlation id (session.CorrelationID,
// reaching us through policy.EvalContext). A marker carried by the statement
// itself wins, because it is per-statement and the session's is per-
// connection: an agent whose task 3 and task 9 run byte-identical SQL needs to
// say so per statement, and one HTTP connection carries many requests.
//
// Each protocol supplies that per-statement marker the way it can. SQL uses
// the leading -- x-hoop-correlation-id= comment; HTTP uses the
// X-Hoop-Correlation-Id header, since it has no comment syntax to prepend
// one to. Both land here as the same string and are treated identically from
// this point on.
func identify(stmt hoopinspect.Statement, sessionMarker string) statementIdentity {
	canonical, marker, ok, why := canonicalFor(stmt)
	if marker == "" {
		marker = sessionMarker
	}
	id := statementIdentity{
		Canonical:  canonical,
		Marker:     marker,
		Observable: ok,
		Why:        why,
	}
	if ok {
		id.Hash = execKey(canonical)
	}
	return id
}

// parameterized is the message the gate refuses when it can see a statement's
// SHAPE but not the values that will be bound to it.
const parameterized = "the relay can read this statement but not the parameter values " +
	"that will be bound to it, so an approval could not cover what will actually run; " +
	"issue it as a literal statement instead"

// canonicalFor renders one statement as the exact text an approval covers.
//
// ok=false means the gate cannot see everything that will execute, so no hash
// is computed and no approval can ever be claimed or filed for it. Failing
// here is the intended direction: a key over a statement whose payload the
// gate did not read would authorize every payload that follows.
//
// # An allowlist, not a denylist
//
// A statement is observable only when this function RECOGNIZES the message
// kind that produced it. That is the opposite of listing the parameterized
// paths and refusing those, and the difference matters: when a codec grows a
// new message kind — or a new codec is registered — the unknown kind falls
// through to a refusal rather than being silently gated on a shape hash. A
// missing entry here costs an operator a confusing denial; a missing entry in
// a denylist costs an approval that authorizes every later binding.
func canonicalFor(stmt hoopinspect.Statement) (canonical, marker string, ok bool, why string) {
	switch stmt.Protocol {
	case hoopinspect.HTTP:
		d := stmt.HTTP
		if d == nil {
			return "", "", false, "the relay did not decode this request"
		}
		if d.BodyTruncated {
			return "", "", false, "the request body was truncated by the proxy, so an approval " +
				"could not cover what will actually be sent"
		}
		// Method and request URI (already in Text) plus the body: the whole
		// of what the upstream will act on. Resource is deliberately NOT
		// used here even though policy rules key on it — it collapses ids,
		// which is the shape-vs-exact mistake this package exists to avoid.
		//
		// Headers are NOT part of the canonical text, which is what makes
		// the correlation header safe to accept: it names the unit of work
		// a request belongs to without perturbing the key that authorizes
		// it. Two requests differing only in their correlation id hash the
		// same and are the same authorization question — as they should be.
		//
		// An unusable value is dropped rather than carried, so it falls
		// back to the session marker. Note the asymmetry with SQL, and that
		// it is the right way round: a non-conforming SQL marker stays in
		// the text and lands in the hash, because there it is indisputably
		// statement content. A header is never content, so ignoring it
		// costs nothing but dedupe.
		return strings.TrimSpace(stmt.Text + "\n\n" + d.Body), headerMarker(d.CorrelationID), true, ""

	case hoopinspect.Postgres:
		// Simple Query only. The codec decodes Query and Parse and never
		// reads Bind, so under the extended protocol — what essentially
		// every driver and ORM uses — the gate sees `DELETE FROM t WHERE
		// id = $1` and never the value. Decoding Bind is the correct fix
		// and is follow-up work.
		if stmt.Metadata["pg.message"] != "Query" {
			return "", "", false, parameterized
		}

	case hoopinspect.MSSQL:
		// SQLBatch only. An RPCRequest reaches us as the statement text
		// pulled out of sp_executesql, whose parameters the codec does not
		// decode — the same hole as a postgres Parse, and it fails the same
		// way.
		if stmt.Metadata["mssql.message"] != "SQLBatch" {
			return "", "", false, parameterized
		}

	default:
		return "", "", false, "the relay has no way to render a statement on this protocol " +
			"as the exact text a human would approve"
	}

	canonical, marker = canonicalize(stmt.Text)
	return canonical, marker, true, ""
}
