package sidecartui

import (
	"sort"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
)

// Bounds on what the dashboard keeps. A sidecar runs for months; the screen
// shows the recent past, and the audit trail keeps the rest.
const (
	maxFeed         = 2000
	maxLogs         = 1000
	maxClosed       = 300
	maxReviews      = 300
	sparkSeconds    = 60
	maxStatementLen = 4096
)

// Metadata keys the analyzer writes onto a statement event. They are spelled
// here rather than imported because the analyzer package drags its provider
// registry in, and these are wire names that the audit trail freezes anyway.
const (
	metaRiskLevel  = "risk_level"
	metaRiskAction = "risk_action"
	metaAIStatus   = "ai_status"
	metaAIRule     = "ai_rule"
	metaReviewID   = "review_id"
	metaReviewMode = "review_mode"
	metaActivity   = audit.MetadataActivity
)

// Lane is one listener: what it resolved to at startup, and what crossed it.
type Lane struct {
	Name      string
	Protocol  string
	Listen    string
	Network   string
	Upstream  string
	OPA       string
	Rules     string
	Enforcing bool
	Observing bool
	Masking   bool
	Ready     bool

	Sessions   int
	Statements int
	Denied     int
	Masked     int
	Errors     int
	Notes      []string
}

// Session is one client connection.
type Session struct {
	ID         string
	Lane       string
	Principal  string
	Protocol   string
	Started    time.Time
	Ended      time.Time
	Open       bool
	Statements int
	Denied     int
	Masked     int
	Last       string
	Meta       map[string]string
}

// Duration is how long the session ran, or has run so far.
func (s *Session) Duration(now time.Time) time.Duration {
	if s.Open || s.Ended.IsZero() {
		return now.Sub(s.Started)
	}
	return s.Ended.Sub(s.Started)
}

// Review is a human approval a statement waited on or was released by.
//
// The trail records a review only once the statement it holds is decided, so
// a hold still waiting does not appear until it settles. A lane in "return"
// mode denies at once with the id, and that shows as PENDING.
type Review struct {
	ID        string
	Status    string
	Lane      string
	Principal string
	Statement string
	Mode      string
	Message   string
	First     time.Time
	Last      time.Time
	Hits      int

	// Local is a review this process holds for the person at the
	// terminal (Reviewer), as opposed to one a control plane holds. Its
	// status is the Reviewer's, which knows it before the trail does.
	Local     bool
	DecidedBy string
	Decided   time.Time
}

// System is the process-level facts the startup log announces.
type System struct {
	License      string
	LicenseWarn  bool
	Limits       string
	Detection    bool
	Analyzer     string
	ControlPlane string
	Admin        string
	QueryAPI     bool
	Watching     string
	Analytics    bool
	SessionsSink bool
	Stopping     string
}

// State is everything the dashboard draws, built only from what the daemon
// writes. It is a pure reducer so tests can feed it lines and read it back.
type State struct {
	Started time.Time

	Feed     []audit.Event
	Logs     []LogRecord
	Warnings []LogRecord

	// FeedDropped and LogsDropped count what fell off the front of Feed
	// and Logs, so FeedDropped+i is a stable id for Feed[i] that a view
	// can keep its selection on while the buffer rolls.
	FeedDropped int
	LogsDropped int

	Lanes     map[string]*Lane
	LaneOrder []string

	Sessions map[string]*Session
	Closed   []string // session ids, oldest first

	Reviews     map[string]*Review
	ReviewOrder []string // newest last

	Risk     map[string]int
	AIStatus map[string]int
	System   System

	Statements int
	Denied     int
	Masked     int
	Errors     int

	// Per-second statement and denial counts for the sparkline, a ring
	// indexed by unix second modulo its length.
	sparkAt     [sparkSeconds]int64
	sparkStmt   [sparkSeconds]int
	sparkDenied [sparkSeconds]int
}

// NewState returns an empty state started at now.
func NewState(now time.Time) *State {
	return &State{
		Started:  now,
		Lanes:    map[string]*Lane{},
		Sessions: map[string]*Session{},
		Reviews:  map[string]*Review{},
		Risk:     map[string]int{},
		AIStatus: map[string]int{},
	}
}

func (s *State) lane(name string) *Lane {
	if name == "" {
		return nil
	}
	if l, ok := s.Lanes[name]; ok {
		return l
	}
	l := &Lane{Name: name}
	s.Lanes[name] = l
	s.LaneOrder = append(s.LaneOrder, name)
	return l
}

func (s *State) session(id string, at time.Time) (*Session, bool) {
	if sess, ok := s.Sessions[id]; ok {
		return sess, false
	}
	sess := &Session{ID: id, Started: at, Open: true}
	s.Sessions[id] = sess
	return sess, true
}

// ApplyLog folds one operational log record in.
func (s *State) ApplyLog(r LogRecord) {
	if len(s.Logs) == maxLogs {
		s.LogsDropped++
	}
	s.Logs = appendCapped(s.Logs, r, maxLogs)
	if r.Level == "WARN" || r.Level == "ERROR" {
		s.Warnings = appendCapped(s.Warnings, r, maxLogs)
	}

	switch {
	case strings.HasPrefix(r.Msg, "license: "):
		s.System.License = strings.TrimPrefix(r.Msg, "license: ")
		s.System.LicenseWarn = r.Level != "INFO"
		return
	}

	switch r.Msg {
	case "rule limits":
		s.System.Limits = "guardrails " + r.Get("guardrail_rules") + " · masks " + r.Get("mask_rules")
	case "detection plugin attached":
		s.System.Detection = true
	case "risk analyzer attached":
		s.System.Analyzer = strings.TrimSpace(r.Get("provider") + " " + r.Get("model"))
		if send := r.Get("send"); send != "" {
			s.System.Analyzer += " · send " + send
		}
		if r.Get("fail_open") == "true" {
			s.System.Analyzer += " · fail open"
		}
	case "lane ready":
		l := s.lane(r.Get("listener"))
		if l == nil {
			return
		}
		l.Ready = true
		l.Protocol = r.Get("protocol")
		l.Upstream = r.Get("upstream")
		l.Enforcing = r.Get("enforcing") == "true"
		l.Observing = r.Get("observing") == "true"
		l.Rules = r.Get("rules")
		l.OPA = r.Get("opa")
		l.Masking = r.Get("masking") == "true"
	case "hoop-inspect listening":
		l := s.lane(r.Get("connection"))
		if l == nil {
			return
		}
		l.Listen = r.Get("listen")
		l.Network = r.Get("network")
		if l.Protocol == "" {
			l.Protocol = r.Get("protocol")
		}
		if l.Upstream == "" {
			l.Upstream = r.Get("upstream")
		}
	case "control plane connected":
		s.System.ControlPlane = r.Get("url")
	case "admin endpoint listening":
		s.System.Admin = r.Get("listen")
	case "query API mounted":
		s.System.QueryAPI = true
	case "watching the config file; a rule edit applies without a restart":
		s.System.Watching = r.Get("path")
	case "usage analytics enabled":
		s.System.Analytics = true
	case "the control plane takes session events; sending them":
		s.System.SessionsSink = true
	case "shutting down", "shutting down after listener failure":
		s.System.Stopping = r.Msg
	case "session opened":
		// The relay logs its connections whether or not an audit file is
		// configured, so a trail pointed at /dev/null still shows them.
		id := r.Get("session")
		if id == "" {
			return
		}
		sess, isNew := s.session(id, r.Time)
		if sess.Principal == "" {
			sess.Principal = r.Get("principal")
		}
		if sess.Lane == "" {
			sess.Lane = r.Get("listener")
		}
		if isNew {
			if l := s.lane(sess.Lane); l != nil {
				l.Sessions++
			}
		}
	case "session closed":
		id := r.Get("session")
		if sess, ok := s.Sessions[id]; ok && sess.Open {
			s.closeSession(sess, r.Time)
		}
	}

	// A warning naming a listener belongs on that lane's card: "lane
	// resolved no rules", "upstream certificate verification is DISABLED".
	if r.Level == "WARN" && r.Msg != "session start not recorded" {
		if l, ok := s.Lanes[r.Get("listener")]; ok && r.Get("session") == "" {
			l.Notes = append(l.Notes, r.Msg)
		}
	}
}

func (s *State) closeSession(sess *Session, at time.Time) {
	sess.Open = false
	sess.Ended = at
	s.Closed = append(s.Closed, sess.ID)
	for len(s.Closed) > maxClosed {
		delete(s.Sessions, s.Closed[0])
		s.Closed = s.Closed[1:]
	}
}

// ApplyAudit folds one audit event in.
func (s *State) ApplyAudit(ev audit.Event) {
	if len(ev.Statement) > maxStatementLen {
		ev.Statement = ev.Statement[:maxStatementLen] + "…"
	}
	id := string(ev.SessionID)
	lane := s.lane(ev.Connection)

	switch ev.Kind {
	case audit.KindSessionStart:
		sess, isNew := s.session(id, ev.Timestamp)
		sess.Started = ev.Timestamp
		sess.Principal = ev.Principal
		sess.Lane = ev.Connection
		sess.Protocol = string(ev.Protocol)
		sess.Meta = ev.Metadata
		if isNew && lane != nil {
			lane.Sessions++
		}
	case audit.KindSessionEnd:
		sess, isNew := s.session(id, ev.Timestamp.Add(-ev.Duration))
		if isNew && lane != nil {
			lane.Sessions++
		}
		fill(sess, ev)
		if ev.StatementCount > sess.Statements {
			sess.Statements = ev.StatementCount
		}
		if ev.DeniedCount > sess.Denied {
			sess.Denied = ev.DeniedCount
		}
		if sess.Open {
			s.closeSession(sess, ev.Timestamp)
		}
	case audit.KindStatement, audit.KindViolation:
		s.Statements++
		s.tick(ev.Timestamp, ev.Kind == audit.KindViolation)
		if lane != nil {
			lane.Statements++
		}
		if sess, ok := s.Sessions[id]; ok {
			fill(sess, ev)
			sess.Statements++
			sess.Last = ev.Statement
		}
		if ev.Kind == audit.KindViolation {
			s.Denied++
			if lane != nil {
				lane.Denied++
			}
			if sess, ok := s.Sessions[id]; ok {
				sess.Denied++
			}
		}
		if lvl := ev.Metadata[metaRiskLevel]; lvl != "" {
			s.Risk[lvl]++
		}
		if st := ev.Metadata[metaAIStatus]; st != "" {
			s.AIStatus[st]++
		}
		if rid := ev.Metadata[metaReviewID]; rid != "" {
			s.applyReview(rid, ev)
		}
	case audit.KindMasked:
		s.Masked += max(ev.MaskedCount, 1)
		if lane != nil {
			lane.Masked += max(ev.MaskedCount, 1)
		}
		if sess, ok := s.Sessions[id]; ok {
			sess.Masked += max(ev.MaskedCount, 1)
		}
	case audit.KindError:
		s.Errors++
		if lane != nil {
			lane.Errors++
		}
	}
	if len(s.Feed) == maxFeed {
		s.FeedDropped++
	}
	s.Feed = appendCapped(s.Feed, ev, maxFeed)
}

// fill copies identity onto a session first seen mid-flight, which happens
// when the TUI attaches to an audit file that was already being written.
func fill(sess *Session, ev audit.Event) {
	if sess.Principal == "" {
		sess.Principal = ev.Principal
	}
	if sess.Lane == "" {
		sess.Lane = ev.Connection
	}
	if sess.Protocol == "" {
		sess.Protocol = string(ev.Protocol)
	}
}

func (s *State) applyReview(id string, ev audit.Event) {
	r, ok := s.Reviews[id]
	if !ok {
		r = &Review{ID: id, First: ev.Timestamp}
		s.Reviews[id] = r
		s.ReviewOrder = append(s.ReviewOrder, id)
		for len(s.ReviewOrder) > maxReviews {
			delete(s.Reviews, s.ReviewOrder[0])
			s.ReviewOrder = s.ReviewOrder[1:]
		}
	} else {
		// Move to the newest end: a review that just settled is the one
		// the operator is looking for.
		for i, rid := range s.ReviewOrder {
			if rid == id {
				s.ReviewOrder = append(s.ReviewOrder[:i], s.ReviewOrder[i+1:]...)
				break
			}
		}
		s.ReviewOrder = append(s.ReviewOrder, id)
	}
	r.Hits++
	r.Last = ev.Timestamp
	r.Lane = ev.Connection
	r.Principal = ev.Principal
	r.Mode = ev.Metadata[metaReviewMode]
	r.Message = ev.Message
	if r.Local {
		// The Reviewer's word stands: it filed the review, so its
		// statement carries the request body the trail leaves out.
		return
	}
	r.Statement = ev.Statement
	r.Status = reviewStatus(ev)
}

// ApplyLocalReview folds in a review the terminal's Reviewer filed or
// settled. It arrives before any audit event names it: the hold is still
// waiting when it is filed, and the trail records it only once it settles.
func (s *State) ApplyLocalReview(lr LocalReview) {
	r, ok := s.Reviews[lr.ID]
	if !ok {
		r = &Review{ID: lr.ID, First: lr.Filed, Last: lr.Filed}
		s.Reviews[lr.ID] = r
		s.ReviewOrder = append(s.ReviewOrder, lr.ID)
		for len(s.ReviewOrder) > maxReviews {
			delete(s.Reviews, s.ReviewOrder[0])
			s.ReviewOrder = s.ReviewOrder[1:]
		}
	}
	r.Local = true
	r.Lane = lr.Listener
	r.Statement = lr.Statement
	r.Status = lr.Status
	r.DecidedBy = lr.DecidedBy
	r.Decided = lr.Decided
	if lr.Decided.After(r.Last) {
		r.Last = lr.Decided
	}
}

// NextLocalPending is the oldest local review still waiting, skipping the
// ones the person put aside, or "".
func (s *State) NextLocalPending(skip map[string]bool) string {
	for _, id := range s.ReviewOrder {
		r := s.Reviews[id]
		if r != nil && r.Local && r.Status == statusPending && !skip[id] {
			return id
		}
	}
	return ""
}

// Principals lists who has a connection open on a lane, for the approval
// dialog: the reviewer is not told who sent a statement, so the dialog
// shows who could have.
func (s *State) Principals(lane string) []string {
	seen := map[string]bool{}
	var out []string
	for _, sess := range s.OpenSessions() {
		if sess.Lane == lane && sess.Principal != "" && !seen[sess.Principal] {
			seen[sess.Principal] = true
			out = append(out, sess.Principal)
		}
	}
	sort.Strings(out)
	return out
}

// reviewStatus reads where a review stands from the verdict it produced.
//
// The trail carries the id and the denial message, not the backend's status
// word. The message is worded from that status by the analyzer
// (analyzer.reviewReason), so the clauses below are its vocabulary.
func reviewStatus(ev audit.Event) string {
	if ev.Allowed {
		return "APPROVED"
	}
	m := strings.ToLower(ev.Message)
	switch {
	case strings.Contains(m, "waiting for approval"):
		return "PENDING"
	case strings.Contains(m, "rejected"):
		return "REJECTED"
	case strings.Contains(m, "revoked"):
		return "REVOKED"
	case strings.Contains(m, "already used"):
		return "EXECUTED"
	case strings.Contains(m, "connection ended"):
		return "ABANDONED"
	}
	return "DENIED"
}

func (s *State) tick(at time.Time, denied bool) {
	sec := at.Unix()
	i := int(sec % sparkSeconds)
	if s.sparkAt[i] != sec {
		s.sparkAt[i] = sec
		s.sparkStmt[i] = 0
		s.sparkDenied[i] = 0
	}
	s.sparkStmt[i]++
	if denied {
		s.sparkDenied[i]++
	}
}

// Spark returns statements and denials per second for the last
// sparkSeconds seconds, oldest first.
func (s *State) Spark(now time.Time) (stmts, denied []int) {
	stmts = make([]int, sparkSeconds)
	denied = make([]int, sparkSeconds)
	end := now.Unix()
	for k := range sparkSeconds {
		sec := end - int64(sparkSeconds-1-k)
		i := int(sec % sparkSeconds)
		if s.sparkAt[i] == sec {
			stmts[k] = s.sparkStmt[i]
			denied[k] = s.sparkDenied[i]
		}
	}
	return stmts, denied
}

// OpenSessions returns the open sessions, newest first.
func (s *State) OpenSessions() []*Session {
	var out []*Session
	for _, sess := range s.Sessions {
		if sess.Open {
			out = append(out, sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// SessionList returns open sessions first, newest first, then closed ones,
// most recently closed first.
func (s *State) SessionList() []*Session {
	out := s.OpenSessions()
	for i := len(s.Closed) - 1; i >= 0; i-- {
		if sess, ok := s.Sessions[s.Closed[i]]; ok {
			out = append(out, sess)
		}
	}
	return out
}

// Active counts the open sessions on one lane.
func (s *State) Active(lane string) int {
	n := 0
	for _, sess := range s.Sessions {
		if sess.Open && sess.Lane == lane {
			n++
		}
	}
	return n
}

// PendingReviews counts reviews whose last word was PENDING.
func (s *State) PendingReviews() int {
	n := 0
	for _, r := range s.Reviews {
		if r.Status == "PENDING" {
			n++
		}
	}
	return n
}

func appendCapped[T any](s []T, v T, limit int) []T {
	s = append(s, v)
	if len(s) > limit {
		// Copy down rather than reslice, so the backing array does not
		// grow without bound over a long run.
		n := copy(s, s[len(s)-limit:])
		s = s[:n]
	}
	return s
}
