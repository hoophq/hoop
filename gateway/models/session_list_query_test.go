package models

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/aws/smithy-go/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// normalizeSQL collapses the fragments' indentation so a golden string can pin
// which predicates are emitted, in what order, without being brittle about
// whitespace.
func normalizeSQL(s string) string { return strings.Join(strings.Fields(s), " ") }

// The shape decides which SQL the count and the page share, so its rules are
// worth pinning down: getting reviewsDriven wrong silently changes which
// sessions the endpoint returns, and emitting a filter that is not set costs
// the planner an index.
func TestNewSessionListShape(t *testing.T) {
	optWith := func(mutate func(*SessionOption)) SessionOption {
		opt := NewSessionOption()
		mutate(&opt)
		return opt
	}

	for _, tt := range []struct {
		msg   string
		admin bool
		opt   SessionOption
		want  sessionListShape
	}{
		{
			msg:   "an admin with no filters at all touches nothing but org_id",
			admin: true,
			opt:   NewSessionOption(),
			want:  sessionListShape{isAdmin: true},
		},
		{
			msg:   "a regular user always needs reviews, for the visibility check",
			admin: false,
			opt:   NewSessionOption(),
			want:  sessionListShape{needsReviews: true},
		},
		{
			msg:   "a concrete review status drives the query from reviews and compares by equality",
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.ReviewStatus = "PENDING" }),
			want: sessionListShape{
				isAdmin:            true,
				reviewStatusActive: true,
				needsReviews:       true,
				reviewsDriven:      true,
				reviewStatusExact:  true,
			},
		},
		{
			msg:   "a wildcard review status still drives from reviews but cannot use equality",
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.ReviewStatus = "PEND%" }),
			want: sessionListShape{
				isAdmin:            true,
				reviewStatusActive: true,
				needsReviews:       true,
				reviewsDriven:      true,
			},
		},
		{
			msg: "an empty review status selects sessions without a review, so reviews cannot drive it",
			// ?review.status= is a real filter: COALESCE(rv.status,'') LIKE ''
			// matches only sessions that have no review at all. An INNER JOIN
			// would return nothing instead.
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.ReviewStatus = "" }),
			want: sessionListShape{
				isAdmin:            true,
				reviewStatusActive: true,
				needsReviews:       true,
			},
		},
		{
			msg:   "a review status of only wildcards can match a session without a review",
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.ReviewStatus = "%%" }),
			want: sessionListShape{
				isAdmin:            true,
				reviewStatusActive: true,
				needsReviews:       true,
			},
		},
		{
			msg:   "an approver filter implies the session has a review",
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.ReviewApproverEmail = ptr.String("someone@hoop.dev") }),
			want: sessionListShape{
				isAdmin:        true,
				approverActive: true,
				needsReviews:   true,
				reviewsDriven:  true,
			},
		},
		{
			msg: "a status that is not an enum label falls back to the text comparison",
			// Casting it to enum_reviews_status would raise "invalid input
			// value for enum" and turn an empty page into a 500.
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.ReviewStatus = "NOT_A_STATUS" }),
			want: sessionListShape{
				isAdmin:            true,
				reviewStatusActive: true,
				needsReviews:       true,
				reviewsDriven:      true,
			},
		},
		{
			msg:   "an underscore is a LIKE metacharacter, so equality is not equivalent",
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.ReviewStatus = "PENDIN_" }),
			want: sessionListShape{
				isAdmin:            true,
				reviewStatusActive: true,
				needsReviews:       true,
				reviewsDriven:      true,
			},
		},
		{
			msg:   "a set connection filter is emitted, the unset ones are not",
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.ConnectionName = "pg-prod" }),
			want: sessionListShape{
				isAdmin:           true,
				sessionConditions: []string{sessionListConnectionCondition},
			},
		},
		{
			msg: "an explicitly empty user filter is a real filter, not the unset sentinel",
			// ?user= selects sessions with no user; only "%" may be dropped.
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.User = "" }),
			want: sessionListShape{
				isAdmin:           true,
				sessionConditions: []string{sessionListUserCondition},
			},
		},
		{
			msg:   "an empty jira key slice contributes nothing",
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.JiraIssueKey = []string{} }),
			want:  sessionListShape{isAdmin: true},
		},
		{
			msg:   "end_date without start_date does not emit the range, matching the old CASE guard",
			admin: true,
			opt: optWith(func(o *SessionOption) {
				o.EndDate = sql.NullString{String: "2026-01-01T00:00:00Z", Valid: true}
			}),
			want: sessionListShape{isAdmin: true},
		},
		{
			msg:   "the live filter emits status and verb as literals, so a partial index can apply",
			admin: true,
			opt: optWith(func(o *SessionOption) {
				o.Status = "open"
				o.Verb = "connect"
			}),
			want: sessionListShape{
				isAdmin: true,
				sessionConditions: []string{
					sessionListStatusConditions["open"],
					sessionListVerbConditions["connect"],
				},
			},
		},
		{
			msg:   "an input filter emits the blob predicate",
			admin: true,
			opt:   optWith(func(o *SessionOption) { o.Input = "delete" }),
			want: sessionListShape{
				isAdmin:           true,
				sessionConditions: []string{sessionListInputCondition},
			},
		},
		{
			msg:   "every session filter set emits every session predicate, in a stable order",
			admin: true,
			opt: optWith(func(o *SessionOption) {
				o.User = "alice"
				o.ConnectionName = "pg-prod"
				o.ConnectionType = "database"
				o.BatchID = ptr.String("b")
				o.CorrelationID = ptr.String("c")
				o.JiraIssueKey = []string{"ENG-1"}
				o.StartDate = sql.NullString{String: "2026-01-01T00:00:00Z", Valid: true}
				o.Status = "done"
				o.Verb = "exec"
				o.Input = "orders"
			}),
			want: sessionListShape{
				isAdmin: true,
				sessionConditions: []string{
					sessionListUserCondition,
					sessionListConnectionCondition,
					sessionListConnectionTypeCondition,
					sessionListBatchIDCondition,
					sessionListCorrelationIDCondition,
					sessionListJiraCondition,
					sessionListDateRangeCondition,
					sessionListStatusConditions["done"],
					sessionListVerbConditions["exec"],
					sessionListInputCondition,
				},
			},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			assert.Equal(t, tt.want, newSessionListShape(tt.admin, tt.opt))
		})
	}
}

// Golden WHERE clauses. A substring assertion passes whether a predicate is
// ANDed, ORed, duplicated or commented out; comparing the whole normalized
// clause is what actually catches a dropped or reordered predicate.
func TestSessionListShapeWhereGolden(t *testing.T) {
	const visibility = `CASE WHEN s.user_id != @user_id THEN EXISTS ( ` +
		`SELECT 1 FROM private.users u ` +
		`INNER JOIN private.user_groups ug ON ug.user_id = u.id ` +
		`INNER JOIN private.review_groups rg ON rg.group_name = ug.name ` +
		`WHERE rg.review_id = rv.id AND NOT rg.added_on_denial AND u.subject = @user_id ) ELSE true END`

	for _, tt := range []struct {
		msg   string
		admin bool
		opt   SessionOption
		want  string
	}{
		{
			msg:   "the default admin listing is a single org predicate",
			admin: true,
			opt:   NewSessionOption(),
			// This is the plan-critical case: GET /api/sessions with no query
			// string. Any tautology added here costs the count its index.
			want: `s.org_id = @org_id`,
		},
		{
			msg:   "a regular user adds only the visibility subtree",
			admin: false,
			opt:   NewSessionOption(),
			want:  `s.org_id = @org_id AND ` + visibility,
		},
		{
			msg:   "a concrete review status constrains the driving table and compares by enum",
			admin: true,
			opt:   SessionOption{User: "%", ConnectionType: "%", ConnectionName: "%", ReviewStatus: "APPROVED", Limit: 20},
			want: `s.org_id = @org_id AND rv.org_id = @org_id AND ` +
				`rv.status = (@review_status)::private.enum_reviews_status`,
		},
		{
			msg:   "an empty review status keeps the COALESCE form and does not constrain rv.org_id",
			admin: true,
			opt:   SessionOption{User: "%", ConnectionType: "%", ConnectionName: "%", ReviewStatus: "", Limit: 20},
			want:  `s.org_id = @org_id AND COALESCE(rv.status::text, '')::TEXT LIKE @review_status`,
		},
		{
			msg:   "an approver filter emits the EXISTS with no IS NOT NULL guard",
			admin: true,
			opt: SessionOption{User: "%", ConnectionType: "%", ConnectionName: "%", ReviewStatus: "%",
				ReviewApproverEmail: ptr.String("a@b.c"), Limit: 20},
			want: `s.org_id = @org_id AND rv.org_id = @org_id AND EXISTS ( ` +
				`SELECT 1 FROM private.users u ` +
				`INNER JOIN private.user_groups ug ON ug.user_id = u.id ` +
				`INNER JOIN private.review_groups rg ON rg.group_name = ug.name ` +
				`WHERE rg.review_id = rv.id AND NOT rg.added_on_denial AND u.email = @review_approver_email )`,
		},
		{
			msg:   "a connection filter emits exactly one session predicate",
			admin: true,
			opt:   SessionOption{User: "%", ConnectionType: "%", ConnectionName: "lt-conn-007", ReviewStatus: "%", Limit: 20},
			want:  `s.org_id = @org_id AND COALESCE(s.connection::text, '') LIKE @connection`,
		},
		{
			msg:   "the live filter binds no parameter for status or verb",
			admin: true,
			opt: SessionOption{User: "%", ConnectionType: "%", ConnectionName: "%", ReviewStatus: "%",
				Status: "open", Verb: "connect", Limit: 20},
			want: `s.org_id = @org_id AND s.status = 'open' AND s.verb = 'connect'`,
		},
		{
			msg:   "an input filter reads the input blob of the same org",
			admin: true,
			opt:   SessionOption{User: "%", ConnectionType: "%", ConnectionName: "%", ReviewStatus: "%", Input: "x", Limit: 20},
			want: `s.org_id = @org_id AND EXISTS ( SELECT 1 FROM private.blobs b ` +
				`WHERE b.org_id = s.org_id AND b.id = s.blob_input_id ` +
				`AND b.blob_stream->>0 ILIKE @input_pattern )`,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			shape := newSessionListShape(tt.admin, tt.opt)
			assert.Equal(t, tt.want, normalizeSQL(shape.where()))
		})
	}
}

func TestSessionListShapeFrom(t *testing.T) {
	for _, tt := range []struct {
		msg   string
		admin bool
		opt   SessionOption
		want  string
	}{
		{
			msg:   "an unfiltered admin listing touches sessions only",
			admin: true,
			opt:   NewSessionOption(),
			want:  `FROM private.sessions s`,
		},
		{
			msg:   "a regular user keeps the LEFT JOIN for the visibility check",
			admin: false,
			opt:   NewSessionOption(),
			want: `FROM private.sessions s ` +
				`LEFT JOIN private.reviews AS rv ON rv.org_id = s.org_id AND rv.session_id = s.id`,
		},
		{
			msg:   "a review filter that cannot match a reviewless session leads with reviews",
			admin: true,
			opt:   SessionOption{User: "%", ConnectionType: "%", ConnectionName: "%", ReviewStatus: "APPROVED", Limit: 20},
			want: `FROM private.reviews AS rv ` +
				`INNER JOIN private.sessions s ON s.org_id = rv.org_id AND s.id = rv.session_id`,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			shape := newSessionListShape(tt.admin, tt.opt)
			assert.Equal(t, tt.want, normalizeSQL(shape.from()))
		})
	}
}

// No shape may reference `rv` without joining reviews, and none may emit a
// clause the parser would reject.
func TestSessionListShapeIsAlwaysWellFormed(t *testing.T) {
	statuses := []string{"%", "", "%%", "PENDING", "PEND%", "PENDIN_", "NOT_A_STATUS"}
	for _, admin := range []bool{true, false} {
		for _, status := range statuses {
			for _, approver := range []*string{nil, ptr.String("a@b.c")} {
				opt := NewSessionOption()
				opt.ReviewStatus = status
				opt.ReviewApproverEmail = approver
				shape := newSessionListShape(admin, opt)
				from, where := normalizeSQL(shape.from()), normalizeSQL(shape.where())

				if strings.Contains(where, "rv.") {
					assert.Contains(t, from, "private.reviews",
						"where references rv but from does not join reviews: %q", where)
				}
				assert.True(t, strings.HasPrefix(where, "s.org_id = @org_id"),
					"every shape must filter by org first, got %q", where)
				assert.False(t, strings.HasSuffix(where, "AND"), "dangling AND in %q", where)
				assert.False(t, strings.Contains(where, "AND AND"), "empty predicate in %q", where)
			}
		}
	}
}

// The default count mode is part of the HTTP contract: it decides what every
// caller that does not send ?count= pays for, and what number they get back.
// Changing it is a breaking API change, so it is pinned here rather than left
// to whatever the constructor happens to say.
func TestDefaultCountModeIsCapped(t *testing.T) {
	assert.Equal(t, SessionCountCapped, DefaultSessionCountMode,
		"flipping this default changes GET /api/sessions for every existing client")

	// Both ways of building an option must land on the same default; a struct
	// literal silently keeping the expensive mode is the failure this guards.
	assert.Equal(t, DefaultSessionCountMode, NewSessionOption().CountMode)
	assert.Equal(t, DefaultSessionCountMode, resolveCountMode(SessionOption{}.CountMode))

	// An explicit choice always wins over the default.
	for _, mode := range []SessionCountMode{SessionCountExact, SessionCountNone, SessionCountCapped} {
		assert.Equal(t, mode, resolveCountMode(mode))
	}
}

func TestParseSessionCountMode(t *testing.T) {
	for _, tt := range []struct {
		in      string
		want    SessionCountMode
		wantErr bool
	}{
		{in: "exact", want: SessionCountExact},
		{in: "none", want: SessionCountNone},
		{in: "capped", want: SessionCountCapped},
		{in: "", wantErr: true},
		{in: "Exact", wantErr: true},
		{in: "1", wantErr: true},
	} {
		t.Run("count="+tt.in, func(t *testing.T) {
			got, err := ParseSessionCountMode(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The model guards the values itself, because the shape drops a status or verb
// it has no literal for. Without the guard that drop would widen the result.
func TestListSessionsRejectsUnknownStatusAndVerb(t *testing.T) {
	for _, opt := range []SessionOption{
		{Status: "running", Limit: 20},
		{Status: "OPEN", Limit: 20},
		{Verb: "plain-exec", Limit: 20},
	} {
		_, err := ListSessions("org", "user", true, opt)
		require.Error(t, err, "%+v", opt)
	}
	for _, v := range []string{"open", "ready", "done"} {
		assert.True(t, IsValidSessionStatus(v), v)
	}
	for _, v := range []string{"connect", "exec"} {
		assert.True(t, IsValidSessionVerb(v), v)
	}
}

// Only % is a wildcard, as the API documents: a search for user_id must not
// match userXid, and a backslash must not escape the next character.
func TestSessionInputPattern(t *testing.T) {
	for in, want := range map[string]string{
		"delete":        `%delete%`,
		"delete%orders": `%delete%orders%`,
		"user_id":       `%user\_id%`,
		`C:\temp`:       `%C:\\temp%`,
		`a\`:            `%a\\%`,
	} {
		assert.Equal(t, want, sessionInputPattern(in), in)
	}
	assert.NoError(t, ValidateSessionInputFilter(""))
	assert.NoError(t, ValidateSessionInputFilter(strings.Repeat("x", MaxSessionListInputLength)))
	assert.Error(t, ValidateSessionInputFilter(strings.Repeat("x", MaxSessionListInputLength+1)))
}
