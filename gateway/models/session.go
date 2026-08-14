package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// indicates the blob is stored in database wire protocol
const BlobFormatWireProtoType string = "wire-proto"

type BlobInputType string

func (b *BlobInputType) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to cast blob input to []byte, got=%T", value)
	}
	result := []string{}
	err := json.Unmarshal(bytes, &result)
	if err != nil {
		return fmt.Errorf("failed decoding blob input to []string: %v", err)
	}
	if len(result) == 0 {
		return nil
	}
	*b = BlobInputType(result[0])
	return nil
}

const (
	// MaxSessionListLimit is the largest page the session list will serve.
	MaxSessionListLimit = 100
	// MaxSessionListOffset bounds how deep a caller may paginate. At the
	// maximum limit that is still 100 pages; anything beyond it should be
	// expressed as a date or connection filter instead, because the cost of an
	// offset grows with its depth no matter how the query is written.
	MaxSessionListOffset = 10000
	// SessionCountCapValue is the total reported by SessionCountCapped once the
	// real total is known to exceed it, i.e. "10000+".
	SessionCountCapValue = 10000
	// sessionCountCapLimit is where the capped count stops scanning. One more
	// than the reported value, so reaching it proves there is at least one row
	// beyond SessionCountCapValue.
	sessionCountCapLimit = SessionCountCapValue + 1
)

// SessionCountMode selects how expensive a total the caller is willing to pay
// for. The count is a separate statement from the page and, on a large tenant,
// costs about as much as the page itself: it has no LIMIT, so it visits every
// row matching the filter regardless of the page size.
type SessionCountMode string

const (
	// SessionCountExact counts every matching row. The most expensive mode, and
	// the only one that can drive a "page N of M" control.
	SessionCountExact SessionCountMode = "exact"
	// SessionCountNone skips the count statement entirely and leaves Total nil.
	// Callers that only need to paginate should use this and rely on
	// HasNextPage.
	SessionCountNone SessionCountMode = "none"
	// SessionCountCapped stops counting at sessionCountCapLimit and reports
	// SessionCountCapValue with TotalIsCapped set.
	SessionCountCapped SessionCountMode = "capped"

	// DefaultSessionCountMode is what a caller gets when it does not choose one.
	//
	// Capped rather than exact: the count statement has no LIMIT, so an exact
	// count visits every row matching the filter however small the page is, and
	// on a large tenant that one statement was 98.8% of the endpoint's database
	// time. Capping is safe as a default because it never reports a wrong
	// number — below the cap it is the exact total with TotalIsCapped false, and
	// above it Total is a floor that TotalIsCapped explicitly marks as such. A
	// caller that needs the precise figure asks for SessionCountExact.
	DefaultSessionCountMode = SessionCountCapped
)

// resolveCountMode applies the default to an option that did not choose a mode.
// NewSessionOption and ListSessions both go through it so a caller that built a
// SessionOption as a struct literal and one that used the constructor cannot end
// up on different defaults — an unnoticed exact count is precisely how this cost
// stayed invisible until it took a gateway down.
func resolveCountMode(m SessionCountMode) SessionCountMode {
	if m == "" {
		return DefaultSessionCountMode
	}
	return m
}

// ParseSessionCountMode validates a count mode coming from an API caller.
func ParseSessionCountMode(v string) (SessionCountMode, error) {
	switch SessionCountMode(v) {
	case SessionCountExact:
		return SessionCountExact, nil
	case SessionCountNone:
		return SessionCountNone, nil
	case SessionCountCapped:
		return SessionCountCapped, nil
	}
	return "", fmt.Errorf("invalid count value %q, expected one of %v", v,
		[]SessionCountMode{SessionCountExact, SessionCountNone, SessionCountCapped})
}

type SessionOption struct {
	User                string
	ConnectionType      string
	ConnectionName      string
	ReviewStatus        string
	ReviewApproverEmail *string
	BatchID             *string
	CorrelationID       *string
	JiraIssueKey        []string
	StartDate           sql.NullString
	EndDate             sql.NullString
	Offset              int
	Limit               int
	CountMode           SessionCountMode
}

func NewSessionOption() SessionOption {
	return SessionOption{
		User:           "%",
		ConnectionType: "%",
		ConnectionName: "%",
		ReviewStatus:   "%",
		Limit:          20,
		Offset:         0,
		CountMode:      resolveCountMode(""),
	}
}

type SessionAIAnalysis struct {
	RiskLevel   string                  `json:"risk_level"`
	Title       string                  `json:"title"`
	Explanation string                  `json:"explanation"`
	Action      string                  `json:"action"`
	Summary     string                  `json:"summary,omitempty"`
	Model       string                  `json:"model,omitempty"`
	Steps       []SessionAIAnalysisStep `json:"steps,omitempty"`
}

// SessionAIAnalysisStep is one step of the agentic analyzer investigation trace.
type SessionAIAnalysisStep struct {
	Type       string    `json:"type"`
	Thinking   string    `json:"thinking,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolInput  string    `json:"tool_input,omitempty"`
	ToolOutput string    `json:"tool_output,omitempty"`
	IsError    bool      `json:"is_error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// SessionGuardRailMatchedRule mirrors guardrails.Rule and represents the specific
// internal rule entry that triggered the match.
type SessionGuardRailMatchedRule struct {
	Type         string   `json:"type"`
	Words        []string `json:"words,omitempty"`
	PatternRegex string   `json:"pattern_regex,omitempty"`
}

type SessionGuardRailsInfo struct {
	RuleName     string                      `json:"rule_name"`
	Rule         SessionGuardRailMatchedRule `json:"rule"`
	Direction    string                      `json:"direction"`
	MatchedWords []string                    `json:"matched_words"`
	// Message is the admin-defined message configured on the matched rule entry,
	// resolved gateway-side. Empty when the matched rule has no custom message.
	Message string `json:"message"`
}

type Session struct {
	ID                   string                  `gorm:"column:id"`
	OrgID                string                  `gorm:"column:org_id"`
	Connection           string                  `gorm:"column:connection"`
	ResourceName         string                  `gorm:"column:resource_name;->"`
	ConnectionType       string                  `gorm:"column:connection_type"`
	ConnectionSubtype    string                  `gorm:"column:connection_subtype"`
	ConnectionTags       map[string]string       `gorm:"column:connection_tags;serializer:json"`
	Verb                 string                  `gorm:"column:verb"`
	Labels               map[string]string       `gorm:"column:labels;serializer:json"`
	Metadata             map[string]any          `gorm:"column:metadata;serializer:json"`
	IntegrationsMetadata map[string]any          `gorm:"column:integrations_metadata;serializer:json"`
	Metrics              map[string]any          `gorm:"column:metrics;serializer:json"`
	AIAnalysis           *SessionAIAnalysis      `gorm:"column:ai_analysis;serializer:json"`
	GuardRailsInfo       []SessionGuardRailsInfo `gorm:"column:guardrails_info;serializer:json"`
	BlobInputID          sql.NullString          `gorm:"column:blob_input_id"`
	BlobInput            BlobInputType           `gorm:"-"`
	BlobInputSize        int64                   `gorm:"column:blob_input_size;->"`
	BlobStream           *Blob                   `gorm:"-"`
	BlobStreamSize       int64                   `gorm:"column:blob_stream_size;->"`
	UserID               string                  `gorm:"column:user_id"`
	UserName             string                  `gorm:"column:user_name"`
	UserEmail            string                  `gorm:"column:user_email"`
	Status               string                  `gorm:"column:status"`
	ExitCode             *int                    `gorm:"column:exit_code"`
	Review               *SessionReview          `gorm:"column:review;->"`
	SessionBatchID       *string                 `gorm:"column:session_batch_id"`
	MachineIdentityID    *string                 `gorm:"column:machine_identity_id"`
	IdentityType         string                  `gorm:"column:identity_type"`
	CorrelationID        *string                 `gorm:"column:correlation_id"`
	Origin               string                  `gorm:"column:origin"`

	CreatedAt  time.Time  `gorm:"column:created_at"`
	EndSession *time.Time `gorm:"column:ended_at"`
}

type SessionDone struct {
	ID         string
	OrgID      string
	Metrics    map[string]any
	BlobStream json.RawMessage
	BlobFormat *string
	ExitCode   *int
	Status     string
	EndSession *time.Time
}

type SessionList struct {
	// Total is nil when the caller asked for SessionCountNone, i.e. the count
	// was never computed. That is deliberately distinct from a total of zero.
	Total *int64
	// TotalIsCapped reports that Total is a floor (SessionCountCapValue), not
	// the real number of matching rows.
	TotalIsCapped bool
	HasNextPage   bool
	Items         []Session
}

type Blob struct {
	ID         string          `gorm:"column:id"`
	OrgID      string          `gorm:"column:org_id"`
	BlobStream json.RawMessage `gorm:"column:blob_stream"`
	Type       string          `gorm:"column:type"`
	BlobFormat *string         `gorm:"column:format"`
}
type SessionReview struct {
	ID                    string            `json:"id"`
	SessionID             string            `json:"session_id"`
	Type                  string            `json:"type"`
	Status                string            `json:"status"`
	CreatedAt             time.Time         `json:"created_at"`
	RevokedAt             *time.Time        `json:"revoked_at"`
	AccessDurationSec     int64             `json:"access_duration_sec"`
	ReviewGroups          []ReviewGroups    `json:"review_groups" gorm:"review_groups;serializer:json"`
	TimeWindow            *ReviewTimeWindow `json:"time_window" gorm:"time_window;serializer:json;"`
	AccessRequestRuleName *string           `json:"access_request_rule_name"`
	ForceApprovalGroups   pq.StringArray    `json:"force_approval_groups" gorm:"force_approval_groups;serializer:json;"`
	MinApprovals          *int              `json:"min_approvals"`
	RejectionReason       *string           `json:"rejection_reason"`
}

func (r *SessionReview) Scan(value any) error {
	if value == nil {
		return nil
	}

	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("unsupported data type: %T", value)
	}
	return json.Unmarshal(data, r)
}

func (s *Session) GetBlobInput() (BlobInputType, error) {
	var blob Blob
	err := DB.Raw(`
	SELECT b.id, b.org_id, b.blob_stream, b.type, b.format
	FROM private.sessions s
	INNER JOIN private.blobs AS b ON b.type = 'session-input' AND  b.id = s.blob_input_id
	WHERE s.org_id = ? AND s.id = ?`, s.OrgID, s.ID).
		First(&blob).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	result := []string{}
	if err := json.Unmarshal(blob.BlobStream, &result); err != nil {
		return "", fmt.Errorf("failed decoding blob input to []string: %v", err)
	}
	if len(result) == 0 {
		return "", nil
	}
	return BlobInputType(result[0]), nil
}

// GetBlobStream retrieves the blob stream associated with the session
// It returns nil if the session does not have a blob stream associated with it.
func (s *Session) GetBlobStream() (*Blob, error) {
	var blob Blob
	err := DB.Raw(`
	SELECT b.id, b.org_id, b.blob_stream, b.type, b.format
	FROM private.sessions s
	INNER JOIN private.blobs AS b ON b.type = 'session-stream' AND  b.id = s.blob_stream_id
	WHERE s.org_id = ? AND s.id = ?`, s.OrgID, s.ID).
		First(&blob).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &blob, err
}

// Report if the blob is stored as database wire protocol format
func (b Blob) IsWireProtocol() bool { return ptr.ToString(b.BlobFormat) == BlobFormatWireProtoType }

func GetSessionByID(orgID, sid string) (*Session, error) {
	session := &Session{}
	err := DB.Raw(`
	SELECT
		s.id, s.org_id, s.connection, s.connection_type, s.connection_subtype, s.connection_tags, s.verb, s.labels, s.exit_code,
		s.user_id, s.user_name, s.user_email, s.status, s.metadata, s.integrations_metadata, s.metrics, s.session_batch_id,
		s.machine_identity_id, s.identity_type, s.correlation_id, s.origin,
		metrics->>'event_size' AS blob_stream_size, s.blob_input_id, s.ai_analysis, s.guardrails_info,
		octet_length(b.blob_stream::text) - 4 AS blob_input_size, -- sub 4 for the db header
		c.resource_name,
		CASE
			WHEN rv.id IS NULL THEN NULL
			ELSE jsonb_build_object(
				'id', rv.id,
				'type', rv.type,
				'access_duration_sec', rv.access_duration_sec,
				'status', rv.status,
				'time_window', rv.time_window,
				'access_request_rule_name', rv.access_request_rule_name,
				'min_approvals', rv.min_approvals,
				'force_approval_groups', rv.force_approval_groups,
				'rejection_reason', rv.rejection_reason,
				'created_at', to_char(rv.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
				'revoked_at', to_char(rv.revoked_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
				'review_groups', (
					SELECT jsonb_agg(
						jsonb_build_object(
							'id', rg.id,
							'group_name', rg.group_name,
							'status', rg.status,
							'owner_id', rg.owner_id,
							'owner_email', rg.owner_email,
							'owner_name', rg.owner_name,
							'owner_slack_id', rg.owner_slack_id,
							'forced_review', rg.forced_review,
							'reviewed_at', to_char(rg.reviewed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
						)
					)
					FROM private.review_groups AS rg
					WHERE rg.review_id = rv.id
				)
			)
		END AS review,
		s.created_at, s.ended_at
	FROM private.sessions s
	LEFT JOIN private.connections c ON c.org_id = s.org_id AND c.name = s.connection
	LEFT JOIN private.blobs b ON b.id = s.blob_input_id
	LEFT JOIN private.reviews AS rv ON rv.session_id = s.id
	WHERE s.org_id = ? AND s.id = ?
	`, orgID, sid).First(session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return session, nil
}

// The fragments below compose the row set that the count and the page queries
// in ListSessions share. Both MUST be built from the same sessionListShape so
// the reported total always describes the rows the page returns.
//
// They are fragments rather than one constant because the reviews join is not
// always wanted, and the planner cannot work that out for itself: the gateway
// sends bind parameters, so predicates like `(@review_approver_email) IS NOT
// NULL` are opaque at plan time and every `rv` reference survives into the
// plan. The join can only be dropped by emitting different SQL.

// The session-level predicates, each emitted only when the corresponding filter
// is actually set. They reference only `s` (private.sessions).
//
// Emitting an unset filter as a tautology — `... LIKE '%'`, or a CASE that
// collapses to `ELSE true` — is not free, because a bind parameter hides the
// tautology from the planner. It has to assume the LIKE is selective, concludes
// it must examine far more rows than the query asks for, and abandons the index.
// That is what turned the capped COUNT into a parallel sequential scan of
// private.sessions: 470 buffers instead of 75, and under concurrency enough heap
// churn to evict the index pages the page query needs. Dropping the predicate
// entirely is the only way to tell the planner it is not there.
const (
	sessionListUserCondition           = `COALESCE(s.user_id::text, '') LIKE @filter_user_id`
	sessionListConnectionCondition     = `COALESCE(s.connection::text, '') LIKE @connection`
	sessionListConnectionTypeCondition = `COALESCE(s.connection_type::text, '')::TEXT LIKE @connection_type`
	sessionListBatchIDCondition        = `s.session_batch_id = @batch_id`
	sessionListCorrelationIDCondition  = `s.correlation_id = @correlation_id`
	sessionListJiraCondition           = `LOWER(s.integrations_metadata->>'jira_issue_key') = ANY((@jira_issue_keys)::text[])`
	sessionListDateRangeCondition      = `s.created_at BETWEEN @start_date AND @end_date`
)

// sessionListVisibilityCondition restricts a non-admin to their own sessions
// plus the ones they are a reviewer of. References `rv`.
//
// Kept as a CASE rather than folded into `EXISTS(...) OR s.user_id = @user_id`:
// s.user_id is nullable, so `s.user_id != @user_id` is NULL for an ownerless
// session and the CASE falls through to ELSE true. The OR form would evaluate
// to NULL instead and hide those sessions. Only the @is_auditor_or_admin leg is
// dropped, because this fragment is emitted solely when it is false.
const sessionListVisibilityCondition = `
			CASE WHEN s.user_id != @user_id
				THEN
					EXISTS (
						SELECT 1 FROM private.users u
						INNER JOIN private.user_groups ug ON ug.user_id = u.id
						INNER JOIN private.review_groups rg ON rg.group_name = ug.name
						WHERE rg.review_id = rv.id AND u.subject = @user_id
					)
				ELSE true
			END`

// sessionListReviewStatusCondition is the LEFT JOIN form: `rv` may be absent,
// and a session with no review matches whenever the pattern matches ”.
const sessionListReviewStatusCondition = `
			COALESCE(rv.status::text, '')::TEXT LIKE @review_status`

// sessionListReviewStatusConditionExact compares against the enum column so
// index_reviews_org_status applies. A LIKE against a bind parameter has no
// known prefix and can never use an index, and `status::text = @x` cannot
// either, because an enum-to-text cast is only STABLE and so cannot be indexed.
//
// Only emit this for a value that IsValidReviewStatus accepts: casting anything
// else to the enum raises "invalid input value for enum" and would turn today's
// empty page into a 500.
const sessionListReviewStatusConditionExact = `
			rv.status = (@review_status)::private.enum_reviews_status`

// sessionListReviewApproverCondition references `rv`. Emitted only when an
// approver is actually set, so the IS NOT NULL guard is gone with it.
const sessionListReviewApproverCondition = `
			EXISTS (
				SELECT 1 FROM private.users u
				INNER JOIN private.user_groups ug ON ug.user_id = u.id
				INNER JOIN private.review_groups rg ON rg.group_name = ug.name
				WHERE rg.review_id = rv.id AND u.email = @review_approver_email
			)`

// sessionListShape records which SQL the filters in a SessionOption require.
// Both the count and the page derive their FROM and WHERE from one of these,
// which is what keeps `total` consistent with `data`.
type sessionListShape struct {
	// isAdmin skips the visibility subtree, which is a tautology for admins
	// and auditors.
	isAdmin bool
	// reviewStatusActive is false for the "%" sentinel, where the predicate is
	// a tautology (COALESCE never yields NULL, and everything is LIKE '%').
	reviewStatusActive bool
	// approverActive reports whether an approver email was supplied.
	approverActive bool
	// needsReviews is false when nothing left in the query references `rv`, so
	// the reviews join can be dropped altogether.
	needsReviews bool
	// reviewsDriven resolves the row set from private.reviews INNER JOIN
	// sessions instead of scanning sessions in created_at order. Only valid
	// when a session with no review cannot possibly match.
	reviewsDriven bool
	// reviewStatusExact selects the indexable equality form of the status
	// predicate.
	reviewStatusExact bool
	// sessionConditions are the `s`-only predicates whose filters are set. An
	// unset filter contributes nothing rather than a tautology.
	sessionConditions []string
}

func newSessionListShape(isAuditorOrAdmin bool, opt SessionOption) sessionListShape {
	sh := sessionListShape{
		isAdmin: isAuditorOrAdmin,
		// "%" is the unset sentinel, not "". An explicit empty review.status is
		// a real filter that selects sessions with no review at all.
		reviewStatusActive: opt.ReviewStatus != "%",
		approverActive:     opt.ReviewApproverEmail != nil,
	}

	// Same "%"-is-unset rule as review.status: ?user= is a real filter that
	// selects sessions with no user, so only the sentinel may be dropped.
	if opt.User != "%" {
		sh.sessionConditions = append(sh.sessionConditions, sessionListUserCondition)
	}
	if opt.ConnectionName != "%" {
		sh.sessionConditions = append(sh.sessionConditions, sessionListConnectionCondition)
	}
	if opt.ConnectionType != "%" {
		sh.sessionConditions = append(sh.sessionConditions, sessionListConnectionTypeCondition)
	}
	// These mirror the CASE guards they replace exactly: the guard tested the
	// bind parameter for NULL, which is precisely "the caller set this filter".
	if opt.BatchID != nil {
		sh.sessionConditions = append(sh.sessionConditions, sessionListBatchIDCondition)
	}
	if opt.CorrelationID != nil {
		sh.sessionConditions = append(sh.sessionConditions, sessionListCorrelationIDCondition)
	}
	// The old guard also required array_length > 0, which for a non-empty slice
	// is always true.
	if len(opt.JiraIssueKey) > 0 {
		sh.sessionConditions = append(sh.sessionConditions, sessionListJiraCondition)
	}
	// The guard keyed off start_date alone; end_date is defaulted to now by the
	// caller whenever start_date is set.
	if opt.StartDate.Valid {
		sh.sessionConditions = append(sh.sessionConditions, sessionListDateRangeCondition)
	}
	sh.needsReviews = !sh.isAdmin || sh.reviewStatusActive || sh.approverActive

	// A session with no review still matches when the pattern matches the
	// empty string, because the LEFT JOIN form coalesces a missing status to
	// ''. A pattern made only of '%' does that; anything else cannot. An
	// approver filter always requires a review to exist, so it implies the
	// same thing on its own.
	statusExcludesReviewless := sh.reviewStatusActive && strings.Trim(opt.ReviewStatus, "%") != ""
	sh.reviewsDriven = sh.approverActive || statusExcludesReviewless

	// Equality is only equivalent to the LIKE for a pattern free of the three
	// LIKE metacharacters, and it is only safe to express against the enum for
	// a value that is actually one of its labels. An unrecognised status falls
	// back to the text form, which still returns an empty page — just without
	// the index.
	sh.reviewStatusExact = sh.reviewsDriven && sh.reviewStatusActive &&
		!strings.ContainsAny(opt.ReviewStatus, `%_\`) &&
		IsValidReviewStatus(opt.ReviewStatus)
	return sh
}

// from builds the FROM/JOIN clause for the shared row set.
func (sh sessionListShape) from() string {
	switch {
	case sh.reviewsDriven:
		// Leading with reviews turns a scan of every session in the org into a
		// scan of the reviews that match, which for a rare or absent status is
		// the difference between reading a million index entries and reading
		// none. UNIQUE(org_id, session_id) on reviews guarantees the inner join
		// cannot duplicate a session.
		return `
		FROM private.reviews AS rv
		INNER JOIN private.sessions s ON s.org_id = rv.org_id AND s.id = rv.session_id`
	case sh.needsReviews:
		return `
		FROM private.sessions s
		LEFT JOIN private.reviews AS rv ON rv.org_id = s.org_id AND rv.session_id = s.id`
	default:
		return `
		FROM private.sessions s`
	}
}

// where builds the WHERE clause for the shared row set.
func (sh sessionListShape) where() string {
	conditions := []string{`s.org_id = @org_id`}
	if sh.reviewsDriven {
		// Constrain the driving table directly so the org_id index on reviews
		// is usable; the planner will not infer it from s.org_id.
		conditions = append(conditions, `rv.org_id = @org_id`)
	}
	if !sh.isAdmin {
		conditions = append(conditions, sessionListVisibilityCondition)
	}
	if sh.reviewStatusActive {
		if sh.reviewStatusExact {
			conditions = append(conditions, sessionListReviewStatusConditionExact)
		} else {
			conditions = append(conditions, sessionListReviewStatusCondition)
		}
	}
	if sh.approverActive {
		conditions = append(conditions, sessionListReviewApproverCondition)
	}
	conditions = append(conditions, sh.sessionConditions...)
	return "\n\t\t" + strings.Join(conditions, " AND\n\t\t") + "\n"
}

func ListSessions(orgID string, userId string, isAuditorOrAdmin bool, opt SessionOption) (*SessionList, error) {
	// Bounds are enforced here rather than only at the API edge so no caller
	// can reach the pathological plans, and so HasNextPage cannot be derived
	// from a nonsense page size. A limit of 0 used to return an empty page
	// while reporting has_next_page=true forever.
	if opt.Limit < 1 || opt.Limit > MaxSessionListLimit {
		return nil, fmt.Errorf("invalid limit %v, accepted range is 1..%v", opt.Limit, MaxSessionListLimit)
	}
	if opt.Offset < 0 || opt.Offset > MaxSessionListOffset {
		return nil, fmt.Errorf("invalid offset %v, accepted range is 0..%v", opt.Offset, MaxSessionListOffset)
	}
	countMode := resolveCountMode(opt.CountMode)
	if _, err := ParseSessionCountMode(string(countMode)); err != nil {
		return nil, err
	}

	sessionList := &SessionList{Items: []Session{}}
	// Prepare lowercase jira issue keys array
	var jiraIssueKeysLower pq.StringArray
	if len(opt.JiraIssueKey) > 0 {
		jiraIssueKeysLower = make(pq.StringArray, len(opt.JiraIssueKey))
		for i, key := range opt.JiraIssueKey {
			jiraIssueKeysLower[i] = strings.ToLower(key)
		}
	}
	// GORM's named-parameter binder scans the SQL for @name and ignores map
	// keys the statement does not mention, so one set of attributes serves
	// every shape the builder can emit.
	queryAttrs := map[string]any{
		"org_id":                orgID,
		"filter_user_id":        opt.User,
		"connection":            opt.ConnectionName,
		"connection_type":       opt.ConnectionType,
		"review_status":         opt.ReviewStatus,
		"review_approver_email": opt.ReviewApproverEmail,
		"batch_id":              opt.BatchID,
		"correlation_id":        opt.CorrelationID,
		"jira_issue_keys":       jiraIssueKeysLower,
		"start_date":            opt.StartDate,
		"end_date":              opt.EndDate,
		"user_id":               userId,
		// One more row than the caller asked for: its presence is what proves
		// there is a next page, without a second query and without relying on
		// the total.
		"page_limit": opt.Limit + 1,
		"offset":     opt.Offset,
		"count_cap":  sessionCountCapLimit,
	}

	shape := newSessionListShape(isAuditorOrAdmin, opt)
	from, where := shape.from(), shape.where()

	countSessions := func(tx *gorm.DB) error {
		var stmt string
		switch countMode {
		case SessionCountCapped:
			// Stops at sessionCountCapLimit rows instead of counting every
			// matching session. The subquery projects a constant and has no
			// ORDER BY so the scan can stop as soon as the LIMIT is satisfied.
			//
			// Do not add an ORDER BY to make the cap "the first N in page
			// order": measured under a generic plan, that turns an index-only
			// scan that touches 75 buffers into a full parallel sequential scan
			// plus a top-N heapsort over every matching row — 214 ms against
			// 2.5 ms. The cap is a floor on the total, not a page, so the order
			// it counts in carries no meaning.
			stmt = `
		SELECT COUNT(*) FROM (
			SELECT 1` + from + `
			WHERE` + where + `
			LIMIT @count_cap
		) capped`
		default:
			stmt = `
		SELECT COUNT(s.id)` + from + `
		WHERE` + where
		}
		var total int64
		if err := tx.Raw(stmt, queryAttrs).First(&total).Error; err != nil {
			return fmt.Errorf("unable to obtain total count of sessions, reason=%v", err)
		}
		if countMode == SessionCountCapped && total >= sessionCountCapLimit {
			total = SessionCountCapValue
			sessionList.TotalIsCapped = true
		}
		sessionList.Total = &total
		return nil
	}

	listPage := func(tx *gorm.DB) error {
		// Resolve the page of session ids first over sessions + reviews only,
		// then join the heavy parts (connections, blobs, review JSON) against
		// that page. The expensive projections — detoasting the input blob for
		// octet_length and building the review jsonb with its correlated
		// review_groups aggregate — therefore run for at most @page_limit rows
		// instead of every session matching the filter.
		//
		// The org_id predicates on the blobs/reviews joins are required for
		// index use: neither table has an index on its id/session_id column
		// alone, only UNIQUE(org_id, ...) composite ones.
		err := tx.Raw(`
		WITH page AS (
			SELECT s.id`+from+`
			WHERE`+where+`
			ORDER BY s.created_at DESC, s.id DESC
			LIMIT @page_limit
			OFFSET @offset
		)
		SELECT
			s.id, s.org_id, s.connection, s.connection_type, s.connection_subtype, s.connection_tags, s.verb, s.labels, s.exit_code,
			s.user_id, s.user_name, s.user_email, s.status, s.metadata, s.integrations_metadata, s.metrics, s.session_batch_id,
			s.machine_identity_id, s.identity_type, s.correlation_id,
			metrics->>'event_size' AS blob_stream_size, s.blob_input_id, s.blob_stream_id, s.guardrails_info,
			octet_length(b.blob_stream::text) - 4 AS blob_input_size,
			c.resource_name,
			CASE
				WHEN rv.id IS NULL THEN NULL
				ELSE jsonb_build_object(
					'id', rv.id,
					'type', rv.type,
					'access_duration_sec', rv.access_duration_sec,
					'status', rv.status,
					'rejection_reason', rv.rejection_reason,
					'created_at', to_char(rv.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
					'revoked_at', to_char(rv.revoked_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
					'review_groups', (
						SELECT jsonb_agg(
							jsonb_build_object(
								'id', rg.id,
								'group_name', rg.group_name,
								'status', rg.status,
								'owner_id', rg.owner_id,
								'owner_email', rg.owner_email,
								'owner_name', rg.owner_name,
								'owner_slack_id', rg.owner_slack_id,
								'forced_review', rg.forced_review,
								'reviewed_at', to_char(rg.reviewed_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
							)
						)
						FROM private.review_groups AS rg
						WHERE rg.review_id = rv.id
					)
				)
			END AS review,
			s.created_at, s.ended_at
		FROM page
		INNER JOIN private.sessions s ON s.id = page.id
		LEFT JOIN private.connections c ON c.org_id = s.org_id AND c.name = s.connection
		LEFT JOIN private.blobs b ON b.org_id = s.org_id AND b.id = s.blob_input_id
		LEFT JOIN private.reviews AS rv ON rv.org_id = s.org_id AND rv.session_id = s.id
		ORDER BY s.created_at DESC, s.id DESC
		`, queryAttrs).Find(&sessionList.Items).Error
		if err != nil {
			return err
		}
		// The extra row is a probe, not part of the page. Discard it after the
		// ORDER BY so the row dropped is genuinely the first of the next page.
		sessionList.HasNextPage = len(sessionList.Items) > opt.Limit
		if sessionList.HasNextPage {
			sessionList.Items = sessionList.Items[:opt.Limit]
		}
		return nil
	}

	// Without a count there is nothing for the page to be consistent with, so
	// the transaction would only add round trips to the statement whose whole
	// purpose is to be cheap.
	if countMode == SessionCountNone {
		return sessionList, listPage(DB)
	}
	// REPEATABLE READ is what actually makes the two statements share a
	// snapshot. Under the default READ COMMITTED every statement takes a fresh
	// one, so a session inserted between the count and the page would be
	// visible to only one of them and `total` could disagree with `data`.
	// Read-only is declared for the same reason it is true: neither statement
	// writes, and it lets PostgreSQL skip assigning a transaction id.
	return sessionList, DB.Transaction(func(tx *gorm.DB) error {
		if err := countSessions(tx); err != nil {
			return err
		}
		return listPage(tx)
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

// UpsertSession updates or create all attributes of a session with exception of
// session streams
func UpsertSession(sess Session) error {
	if sess.IdentityType == "" {
		sess.IdentityType = "user"
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		// generate deterministic uuid based on the session id to avoid duplicates
		blobInputID := sql.NullString{
			String: uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "blobinput:%s", sess.ID)).String(),
			Valid:  true,
		}

		blobInput := Blob{
			ID:         blobInputID.String,
			OrgID:      sess.OrgID,
			Type:       "session-input",
			BlobStream: json.RawMessage(fmt.Sprintf("[%q]", sess.BlobInput)),
		}
		res := tx.Table("private.blobs").
			Where("org_id = ? AND id = ?", sess.OrgID, blobInputID.String).
			Updates(blobInput)
		if res.Error == nil && res.RowsAffected == 0 {
			res.Error = tx.Table("private.blobs").Create(blobInput).Error
		}

		if res.Error != nil {
			return fmt.Errorf("failed creating session blob input, reason=%v", res.Error)
		}
		return tx.Table("private.sessions").Save(
			Session{
				ID:                   sess.ID,
				OrgID:                sess.OrgID,
				Labels:               sess.Labels,
				Metadata:             sess.Metadata,
				IntegrationsMetadata: sess.IntegrationsMetadata,
				Metrics:              sess.Metrics,
				Connection:           sess.Connection,
				ConnectionType:       sess.ConnectionType,
				ConnectionSubtype:    sess.ConnectionSubtype,
				ConnectionTags:       sess.ConnectionTags,
				Verb:                 sess.Verb,
				UserID:               sess.UserID,
				UserName:             sess.UserName,
				UserEmail:            sess.UserEmail,
				BlobInputID:          blobInputID,
				Status:               sess.Status,
				ExitCode:             sess.ExitCode,
				SessionBatchID:       sess.SessionBatchID,
				MachineIdentityID:    sess.MachineIdentityID,
				IdentityType:         sess.IdentityType,
				CorrelationID:        sess.CorrelationID,
				Origin:               sess.Origin,
				CreatedAt:            sess.CreatedAt,
				EndSession:           sess.EndSession,
				AIAnalysis:           sess.AIAnalysis,
				GuardRailsInfo:       sess.GuardRailsInfo,
			}).Error
	})
}

// UpdateSessionStatus updates only the status of a session
func UpdateSessionStatus(orgID, sessionID, status string) error {
	return DB.Table("private.sessions").
		Where("org_id = ? AND id = ?", orgID, sessionID).
		UpdateColumn("status", status).Error
}

// SetSessionStatusOpenIfReady flips a session from ready to open when its
// execution starts. Reviewed sessions are created ahead of the execution and
// approved into the ready status; without this flip they would keep reporting
// ready while the agent runs them. It is a no-op (false, nil) when the session
// is in any other status.
func SetSessionStatusOpenIfReady(db *gorm.DB, orgID, sessionID string) (bool, error) {
	res := db.Table("private.sessions").
		Where("org_id = ? AND id = ? AND status = ?", orgID, sessionID, "ready").
		UpdateColumn("status", "open")
	return res.RowsAffected > 0, res.Error
}

// SetSessionCredentialsExpireAt stores the credential expiry time in the session metadata.
// The frontend reads metadata.credentials_expire_at to display validity and toggle the connect button.
func SetSessionCredentialsExpireAt(orgID, sessionID string, expireAt time.Time) error {
	value := fmt.Sprintf(`{"credentials_expire_at":%q}`, expireAt.Format(time.RFC3339))
	return DB.Table("private.sessions").
		Where("org_id = ? AND id = ?", orgID, sessionID).
		Update("metadata", gorm.Expr("COALESCE(metadata, '{}'::jsonb) || ?::jsonb", value)).
		Error
}

// SetSessionRevokedAt stores the credential revocation timestamp in the session metadata.
func SetSessionCredentialsRevokedAt(orgID, sessionID string, revokedAt time.Time) error {
	value := fmt.Sprintf(`{"credentials_revoked_at":%q}`, revokedAt.Format(time.RFC3339))
	return DB.Table("private.sessions").
		Where("org_id = ? AND id = ?", orgID, sessionID).
		Updates(map[string]any{
			"metadata": gorm.Expr("COALESCE(metadata, '{}'::jsonb) || ?::jsonb", value),
			"status":   "done",
			"ended_at": revokedAt,
		}).Error
}

// SessionFederationMetadata captures the audit fields IAM Federation writes
// to the session row at SessionOpen time. The shape is intentionally flat
// since downstream consumers (the v1.1 Sessions UI, SIEM exports) want to
// project individual fields without parsing nested JSON.
type SessionFederationMetadata struct {
	// Provider is the federation provider that resolved the session (e.g.
	// gcp_iam).
	Provider string `json:"provider"`
	// HookSource mirrors ConnectionFederationConfig.HookSource. Today only
	// "builtin" is supported; the field is preserved so audit consumers can
	// distinguish hook sources if new ones are added in future releases.
	HookSource string `json:"hook_source"`
	// ResolvedPrincipal is the cloud-side identity the session ran under.
	// For GCP this is the user@org.com address GCP audit logs will attribute
	// the query to.
	ResolvedPrincipal string `json:"resolved_principal"`
	// AdminPrincipal is the impersonator identity (admin SA's client_email
	// for gcp_iam).
	AdminPrincipal string `json:"admin_principal,omitempty"`
	// TokenExpiresAt is the expiration of the credential the agent runs
	// under. Sessions can outlive this; the credential's own expiry is the
	// source of truth for the downstream API.
	TokenExpiresAt time.Time `json:"token_expires_at"`
	// FallbackApplied is true when the primary resolution failed and the
	// configured fallback policy was applied instead (e.g. "static": the
	// session ran on the connection's existing static credentials rather than
	// a federated identity).
	FallbackApplied bool `json:"fallback_applied,omitempty"`
}

// SetSessionFederationMetadata stores the federation audit data under the
// "federation" key inside the session's metadata JSONB. Idempotent: re-running
// with the same fields overwrites them in place via jsonb concatenation.
//
// Errors here are non-fatal for the SessionOpen flow — the caller logs and
// continues. A missing federation entry just means the v1.1 UI won't have
// audit data for that session; the session still opens successfully.
func SetSessionFederationMetadata(orgID, sessionID string, m SessionFederationMetadata) error {
	inner, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed encoding federation metadata: %v", err)
	}
	wrapped := fmt.Sprintf(`{"federation":%s}`, string(inner))
	return DB.Table("private.sessions").
		Where("org_id = ? AND id = ?", orgID, sessionID).
		Update("metadata", gorm.Expr("COALESCE(metadata, '{}'::jsonb) || ?::jsonb", wrapped)).
		Error
}

// SessionStreamBlobID returns the deterministic blob id for a session's stream.
func SessionStreamBlobID(sessionID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "blobstream:%s", sessionID)).String()
}

// CreateEmptySessionStreamBlob inserts an empty stream blob and points the
// session row at it. Subsequent flushes append to that row via AppendSessionStream.
// Idempotent: re-running for the same session is a no-op on conflict.
func CreateEmptySessionStreamBlob(orgID, sessionID string, blobFormat *string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		blobStreamID := SessionStreamBlobID(sessionID)
		blob := Blob{
			ID:         blobStreamID,
			OrgID:      orgID,
			BlobStream: json.RawMessage(`[]`),
			Type:       "session-stream",
			BlobFormat: blobFormat,
		}
		// upsert via UPDATE-then-INSERT pattern used elsewhere in this file
		res := tx.Table("private.blobs").
			Where("org_id = ? AND id = ?", orgID, blobStreamID).
			Updates(map[string]any{"format": blobFormat})
		if res.Error == nil && res.RowsAffected == 0 {
			res.Error = tx.Table("private.blobs").Create(blob).Error
		}
		if res.Error != nil {
			return fmt.Errorf("failed creating empty session stream blob: %v", res.Error)
		}
		return tx.Table("private.sessions").
			Where("org_id = ? AND id = ?", orgID, sessionID).
			Update("blob_stream_id", blobStreamID).Error
	})
}

// AppendSessionStream concatenates entries onto the session's blob_stream
// using Postgres jsonb concatenation. entries must already be a JSON array.
// Returns ErrNotFound if the stream blob row does not exist — callers should
// retry rather than silently dropping the flush window.
func AppendSessionStream(orgID, sessionID string, entries json.RawMessage) error {
	blobStreamID := SessionStreamBlobID(sessionID)
	res := DB.Exec(
		`UPDATE private.blobs SET blob_stream = blob_stream || ?::jsonb WHERE org_id = ? AND id = ?`,
		string(entries), orgID, blobStreamID,
	)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkSessionDone updates the session terminal columns without touching the
// stream blob — flushes write the blob incrementally during the session.
func MarkSessionDone(sess SessionDone) error {
	return DB.Table("private.sessions AS s").
		Where("org_id = ? AND id = ?", sess.OrgID, sess.ID).
		Updates(map[string]any{
			"exit_code": sess.ExitCode,
			"status":    sess.Status,
			"ended_at":  sess.EndSession,
			"metrics":   gorm.Expr("COALESCE(s.metrics, '{}'::jsonb) || ?::jsonb", sess.Metrics),
		}).Error
}

// UpdateSessionEventStream updates a session partially
func UpdateSessionEventStream(sess SessionDone) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// generate deterministic uuid based on the session id to avoid duplicates
		blobStreamID := sql.NullString{
			String: uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "blobstream:%s", sess.ID)).String(),
			Valid:  true,
		}

		blobStream := Blob{
			ID:         blobStreamID.String,
			OrgID:      sess.OrgID,
			BlobStream: sess.BlobStream,
			Type:       "session-stream",
			BlobFormat: sess.BlobFormat,
		}
		res := tx.Table("private.blobs").
			Where("org_id = ? AND id = ?", sess.OrgID, blobStreamID.String).
			Updates(blobStream)
		if res.Error == nil && res.RowsAffected == 0 {
			res.Error = tx.Table("private.blobs").Create(blobStream).Error
		}

		if res.Error != nil {
			return fmt.Errorf("failed creating session blob stream, reason=%v", res.Error)
		}

		// update: status, labels, metrics, end_date, exit_code, event_stream
		return tx.Table("private.sessions AS s").
			Where("org_id = ? AND id = ?", sess.OrgID, sess.ID).
			Updates(map[string]interface{}{
				"blob_stream_id": blobStreamID,
				"exit_code":      sess.ExitCode,
				"status":         sess.Status,
				"ended_at":       sess.EndSession,
				"metrics":        gorm.Expr("COALESCE(s.metrics, '{}'::jsonb) || ?::jsonb", sess.Metrics),
			}).
			Error
	})
}

func UpdateSessionIntegrationMetadata(orgID, sid string, metadata map[string]any) error {
	res := DB.Table("private.sessions").
		Where("org_id = ? AND id = ?", orgID, sid).
		Updates(Session{IntegrationsMetadata: metadata})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func UpdateSessionAnalyzerMetrics(orgID, sid string, metrics map[string]int64) error {
	res := DB.Table("private.sessions AS s").
		Where("s.id = ? AND s.org_id = ?", sid, orgID).
		Update("metrics", gorm.Expr(`
        jsonb_set(
            COALESCE(s.metrics, '{}'::jsonb),
            '{data_analyzer}',
            COALESCE(s.metrics->'data_analyzer', '{}'::jsonb)
            ||
            (
                SELECT jsonb_object_agg(
                    k,
                    to_jsonb(
                        COALESCE((s.metrics->'data_analyzer'->>k)::int, 0) + v::int
                    )
                )
                FROM jsonb_each_text(?::jsonb) AS t(k, v)
            ),
            true
        )
    `, metrics))

	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func UpdateSessionGuardRailsInfo(orgID, sid string, info []byte) error {
	res := DB.Table("private.sessions").
		Where("org_id = ? AND id = ?", orgID, sid).
		Update("guardrails_info", gorm.Expr("COALESCE(guardrails_info, '[]'::jsonb) || ?::jsonb", info))
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func UpdateSessionMetadata(orgID, userEmail, sid string, metadata map[string]any) error {
	res := DB.Table("private.sessions").
		Where("org_id = ? AND id = ? AND user_email = ?", orgID, sid, userEmail).
		Updates(Session{Metadata: metadata})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func UpdateSessionInput(orgID, sid, blobInput string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		blobInputID := uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "blobinput:%s", sid)).String()
		blobInput := Blob{
			ID:         blobInputID,
			OrgID:      orgID,
			Type:       "session-input",
			BlobStream: json.RawMessage(fmt.Sprintf("[%q]", blobInput)),
			BlobFormat: nil,
		}
		res := tx.Table("private.blobs").
			Where("org_id = ? AND id = ?", orgID, blobInputID).
			Updates(blobInput)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func GetSessionJiraIssueByID(orgID, sid string) (string, error) {
	var jiraIssueKey string
	err := DB.Raw(`
	SELECT COALESCE(integrations_metadata->>'jira_issue_key', '')::TEXT FROM private.sessions s
	WHERE s.org_id = ? AND s.id = ?`, orgID, sid).
		First(&jiraIssueKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	return jiraIssueKey, nil
}
