package services

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	sessionwal "github.com/hoophq/hoop/gateway/session/wal"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"gorm.io/gorm"
)

// SidecarSessionEventsFlag turns on POST /api/sidecars/events and the
// handshake header that tells a sidecar to use it.
const SidecarSessionEventsFlag = "experimental.sidecar_session_events"

// sidecarGuardRailRuleType is the rule type a sidecar violation records. The
// event names the rule but not its type, and the session page reads a known
// type as a promise of matched words the event does not carry.
const sidecarGuardRailRuleType = "sidecar"

// The columns a sidecar's own strings land in. A value past them would fail
// the insert on every resend, so the gateway answers it before the database
// does: a long principal is cut, a long connection name is refused.
const (
	maxSidecarSessionIDBytes  = 256
	maxSessionConnectionChars = 128 // sessions.connection VARCHAR(128)
	maxSessionUserChars       = 255 // sessions.user_name, user_email VARCHAR(255)
)

// maxSidecarSessionStreamBytes caps one session's stream like the audit
// plugin caps a session's WAL read. The entry that crosses it is kept, every
// later one is not, and metrics.truncated says so.
const maxSidecarSessionStreamBytes = sessionwal.DefaultMaxRead

// SidecarSessionID names the session one sidecar session is recorded as. It
// is derived, so a resend lands on the same row, and derived from the
// authenticated sidecar, so a token can only ever write its own sessions.
func SidecarSessionID(sidecarID, sessionID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL,
		[]byte("sidecar-session:"+sidecarID+":"+sessionID)).String()
}

// sidecarMirror is the connection a listener is recorded under.
type sidecarMirror struct {
	Name    string
	Type    string
	Subtype string
}

// sidecarMirrorConnection names the connection that mirrors one listener of
// one sidecar, and the type and subtype its protocol maps to.
//
// The mirror connections themselves, and these two rules, belong to the
// change that creates them (Dia 0), which is landing in parallel. This copy
// exists so sessions can name the mirror before that change merges, and it
// is replaced by Dia 0's function then; TestSidecarMirrorConnection pins the
// format both must agree on.
func sidecarMirrorConnection(sidecarName, listener, protocol string) (sidecarMirror, error) {
	out := sidecarMirror{Name: sidecarName + "-" + listener}
	switch protocol {
	case "postgres", "mysql", "mssql", "mongodb":
		out.Type, out.Subtype = "database", protocol
	case "ssh":
		out.Type, out.Subtype = "application", "ssh"
	case "http":
		out.Type, out.Subtype = "httpproxy", "httpproxy"
	case "clickhouse", "grpc", "spanner":
		out.Type, out.Subtype = "custom", protocol
	default:
		return sidecarMirror{}, fmt.Errorf("protocol %q has no connection type", protocol)
	}
	return out, nil
}

// SidecarEventsRefused is a batch, or part of one, the gateway can never
// apply. The handler answers it with a 4xx, so the sidecar does not resend.
type SidecarEventsRefused struct{ Reason string }

func (e SidecarEventsRefused) Error() string { return e.Reason }

// ValidateSidecarSessionEvents refuses a batch the contract does not allow.
func ValidateSidecarSessionEvents(events []daemon.SessionEvent) error {
	for i, e := range events {
		if e.Seq < 1 {
			return SidecarEventsRefused{Reason: fmt.Sprintf("event %d has seq %d; a seq starts at 1", i, e.Seq)}
		}
		if e.Event.SessionID == "" {
			return SidecarEventsRefused{Reason: fmt.Sprintf("event %d has no session_id", i)}
		}
		if len(e.Event.SessionID) > maxSidecarSessionIDBytes {
			return SidecarEventsRefused{Reason: fmt.Sprintf("event %d has a session_id longer than %d bytes",
				i, maxSidecarSessionIDBytes)}
		}
	}
	return nil
}

// SidecarEventsResult counts what a batch did.
type SidecarEventsResult struct {
	// Accepted counts events applied now, the ignored kinds included.
	Accepted int
	// Duplicates counts events at or below a session's last applied seq.
	Duplicates int
}

// sidecarIdentity is the authenticated sidecar a batch came from.
type sidecarIdentity struct {
	ID    string
	Name  string
	OrgID string
}

// sidecarSessionPlan is what one session's events in one batch turn into.
// The writes are applied in field order by applySidecarSessionPlan.
type sidecarSessionPlan struct {
	// SessionID is the gateway's id, SidecarSessionID.
	SessionID string
	// Create is set when the session does not exist yet: the first event
	// creates it, whatever its kind, because a session_start the gateway
	// missed must not lose the rest of the session.
	Create *models.Session
	// Entries is a JSON array of [elapsed, type, base64] to append.
	Entries json.RawMessage
	// GuardRails are appended to guardrails_info.
	GuardRails []models.SessionGuardRailsInfo
	// Masked adds to private.session_metrics, per info type.
	Masked map[string]int64
	// Metrics replaces the metrics column. Nil when nothing applied.
	Metrics map[string]any
	// Sidecar is merged into metadata.sidecar. It always carries last_seq
	// when anything applied.
	Sidecar map[string]any
	// Done ends the session.
	Done *models.SessionDone

	Accepted   int
	Duplicates int
}

// planSidecarSession translates one session's events, in the order the
// sidecar numbered them, into writes. It reads nothing and writes nothing:
// prior is the row as it stands, nil when there is none.
//
// An event at or below the last applied seq is a resend and is skipped. The
// kinds translate as:
//
//	session_start  creates the session (verb connect, status open)
//	statement      an "i" entry with the statement
//	violation      an "i" entry with the statement, an "e" entry with the
//	               denial, and a guardrails_info entry. The gate writes one
//	               event per statement, a violation INSTEAD of a statement,
//	               so the query list would otherwise lose every denied one.
//	error          an "e" entry with the error
//	masked         metrics.data_masking and private.session_metrics
//	session_end    status done, ended_at, and the totals in metadata.sidecar
//	activity       nothing: it carries no content by design (ADR-0015), and
//	               the session page has no place for it
//
// A kind this gateway does not know, from a newer sidecar, is accepted and
// writes nothing, so it never blocks the events behind it.
func planSidecarSession(sc sidecarIdentity, sessionID string, prior *models.SidecarSessionState,
	events []daemon.SessionEvent) (sidecarSessionPlan, error) {
	plan := sidecarSessionPlan{SessionID: SidecarSessionID(sc.ID, sessionID)}

	lastSeq := int64(0)
	if prior != nil {
		lastSeq = prior.LastSeq
	}
	var fresh []daemon.SessionEvent
	for _, e := range events {
		if e.Seq <= lastSeq {
			plan.Duplicates++
			continue
		}
		lastSeq = e.Seq
		fresh = append(fresh, e)
	}
	plan.Accepted = len(fresh)
	if len(fresh) == 0 {
		return plan, nil
	}

	var startedAt time.Time
	metrics := map[string]any{}
	if prior != nil {
		startedAt = prior.CreatedAt
		if prior.Metrics != nil {
			metrics = maps.Clone(prior.Metrics)
		}
	} else {
		sess, err := newSidecarSession(sc, plan.SessionID, sessionID, fresh[0].Event)
		if err != nil {
			return plan, err
		}
		plan.Create = sess
		startedAt = sess.CreatedAt
	}

	stream := sidecarStream{
		startedAt: startedAt,
		size:      metricInt(metrics, "event_size"),
		truncated: metricBool(metrics, "truncated"),
	}
	plan.Sidecar = map[string]any{}
	for _, e := range fresh {
		ev := e.Event
		switch ev.Kind {
		case audit.KindStatement:
			stream.add(ev.Timestamp, "i", ev.Statement)
		case audit.KindViolation:
			stream.add(ev.Timestamp, "i", ev.Statement)
			stream.add(ev.Timestamp, "e", denialText(ev))
			plan.GuardRails = append(plan.GuardRails, sidecarGuardRail(ev))
		case audit.KindError:
			stream.add(ev.Timestamp, "e", ev.Error)
		case audit.KindMasked:
			if ev.MaskedCount <= 0 {
				continue
			}
			if plan.Masked == nil {
				plan.Masked = map[string]int64{}
			}
			plan.Masked[maskedInfoType(ev.MaskedEntities)] += int64(ev.MaskedCount)
		case audit.KindSessionEnd:
			end := ev.Timestamp.UTC()
			plan.Done = &models.SessionDone{
				ID:         plan.SessionID,
				OrgID:      sc.OrgID,
				Status:     "done",
				EndSession: &end,
				// The metrics are written beside the stream; MarkSessionDone
				// merges this into them, and a nil would merge into NULL.
				Metrics: map[string]any{},
			}
			plan.Sidecar["statement_count"] = ev.StatementCount
			plan.Sidecar["denied_count"] = ev.DeniedCount
		}
	}
	plan.Sidecar["last_seq"] = lastSeq

	if len(stream.entries) > 0 {
		plan.Entries = json.RawMessage("[" + strings.Join(stream.entries, ",") + "]")
	}
	metrics["event_size"] = stream.size
	metrics["truncated"] = stream.truncated
	if len(plan.Masked) > 0 {
		addDataMasking(metrics, plan.Masked)
	}
	plan.Metrics = metrics
	return plan, nil
}

// newSidecarSession builds the row a sidecar session is created as, from the
// first event the gateway received for it.
func newSidecarSession(sc sidecarIdentity, id, sidecarSessionID string, ev audit.Event) (*models.Session, error) {
	listener := pgText(ev.Connection)
	sidecarSessionID = pgText(sidecarSessionID)
	if listener == "" {
		return nil, SidecarEventsRefused{Reason: fmt.Sprintf(
			"session %s names no listener in its connection", sidecarSessionID)}
	}
	mirror, err := sidecarMirrorConnection(sc.Name, listener, string(ev.Protocol))
	if err != nil {
		return nil, SidecarEventsRefused{Reason: fmt.Sprintf(
			"session %s on listener %q: %v", sidecarSessionID, listener, err)}
	}
	if utf8.RuneCountInString(mirror.Name) > maxSessionConnectionChars {
		return nil, SidecarEventsRefused{Reason: fmt.Sprintf(
			"session %s: connection name %q is longer than %d characters",
			sidecarSessionID, mirror.Name, maxSessionConnectionChars)}
	}
	// raw whatever the type: proto.SessionRecordingFormat reads custom as a
	// terminal and would replay a clickhouse session as one.
	format := pb.RecordingFormatRaw
	sess := &models.Session{
		ID:                id,
		OrgID:             sc.OrgID,
		Connection:        mirror.Name,
		ConnectionType:    mirror.Type,
		ConnectionSubtype: mirror.Subtype,
		Verb:              pb.ClientVerbConnect,
		RecordingFormat:   &format,
		Status:            "open",
		IdentityType:      plugintypes.IdentityTypeSidecar,
		Origin:            pb.SessionOriginSidecar,
		CreatedAt:         ev.Timestamp.UTC(),
		Metadata: map[string]any{
			"sidecar": map[string]any{
				"id":         sc.ID,
				"name":       sc.Name,
				"listener":   listener,
				"session_id": sidecarSessionID,
				"last_seq":   0,
			},
		},
	}
	// The principal is whatever the sidecar resolved on the wire. An email
	// is filed as one, so the session list can filter on it; anything else
	// (a database role) is a name.
	principal := pgText(ev.Principal)
	if short := truncateChars(principal, maxSessionUserChars); short != principal {
		// The column keeps what fits; the metadata keeps the whole of it.
		sess.Metadata["sidecar"].(map[string]any)["principal"] = principal
		principal = short
	}
	if strings.Contains(principal, "@") {
		sess.UserEmail = principal
	} else {
		sess.UserName = principal
	}
	return sess, nil
}

// pgText drops the NUL bytes Postgres refuses in text and jsonb. JSON
// decoding already made the string valid UTF-8; a NUL is valid UTF-8 and
// still fails the write, on every resend.
func pgText(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}

// truncateChars cuts s to n characters, the unit VARCHAR(n) counts.
func truncateChars(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// sidecarStream builds the entries one batch appends to a session's stream,
// in the audit plugin's format: [seconds since the session started, type,
// base64 of the bytes].
type sidecarStream struct {
	startedAt time.Time
	entries   []string
	size      int64
	truncated bool
}

func (s *sidecarStream) add(at time.Time, kind, text string) {
	if text == "" || s.truncated {
		return
	}
	// A clock step between two events of one session is the sidecar's
	// clock, not a negative duration worth showing.
	elapsed := max(at.Sub(s.startedAt).Seconds(), 0)
	entry, err := json.Marshal([]any{elapsed, kind, base64.StdEncoding.EncodeToString([]byte(text))})
	if err != nil {
		return
	}
	s.entries = append(s.entries, string(entry))
	s.size += int64(len(entry))
	if s.size >= maxSidecarSessionStreamBytes {
		s.truncated = true
	}
}

// denialText is the "e" entry a violation adds after its statement, so the
// query list says the statement did not run.
func denialText(ev audit.Event) string {
	switch {
	case ev.Rule != "" && ev.Message != "":
		return fmt.Sprintf("denied by rule %q: %s", ev.Rule, ev.Message)
	case ev.Rule != "":
		return fmt.Sprintf("denied by rule %q", ev.Rule)
	case ev.Message != "":
		return "denied: " + ev.Message
	}
	return "denied"
}

func sidecarGuardRail(ev audit.Event) models.SessionGuardRailsInfo {
	direction := "input"
	if ev.Direction == inspect.FromServer {
		direction = "output"
	}
	return models.SessionGuardRailsInfo{
		RuleName:     pgText(ev.Rule),
		Rule:         models.SessionGuardRailMatchedRule{Type: sidecarGuardRailRuleType},
		Direction:    direction,
		MatchedWords: []string{},
		Message:      pgText(ev.Message),
	}
}

// maskedInfoType names what a masked event rewrote. The event counts the
// values rewritten across every entity it names, not per entity, so an event
// naming several is filed under all of them joined: splitting the count
// would invent numbers an auditor reads as facts.
func maskedInfoType(entities []string) string {
	if len(entities) == 0 {
		return "unknown"
	}
	sorted := slices.Clone(entities)
	slices.Sort(sorted)
	return strings.Join(slices.Compact(sorted), "+")
}

// addDataMasking adds counts to metrics.data_masking, in the shape the audit
// plugin writes and the session report reads.
func addDataMasking(metrics map[string]any, masked map[string]int64) {
	dm, _ := metrics["data_masking"].(map[string]any)
	if dm == nil {
		dm = map[string]any{"transformed_bytes": int64(0), "err_count": int64(0)}
	} else {
		dm = maps.Clone(dm)
	}
	infoTypes := map[string]any{}
	if prior, ok := dm["info_types"].(map[string]any); ok {
		maps.Copy(infoTypes, prior)
	}
	total := metricInt(dm, "total_redact_count")
	for k, n := range masked {
		infoTypes[k] = metricInt(infoTypes, k) + n
		total += n
	}
	dm["info_types"] = infoTypes
	dm["total_redact_count"] = total
	metrics["data_masking"] = dm
}

// metricInt reads a number from decoded JSON, where it is a float64, or from a
// map this package built, where it is an int64.
func metricInt(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func metricBool(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

// ApplySidecarSessionEvents records a batch. Each session in it is applied in
// its own transaction, so one that fails leaves the others applied.
//
// The error is SidecarEventsRefused when every failure is permanent: the
// sidecar must not resend. Any other error means a resend may succeed, and
// the sessions already applied ignore it by seq.
func ApplySidecarSessionEvents(db *gorm.DB, sc *models.Sidecar, events []daemon.SessionEvent) (SidecarEventsResult, error) {
	var result SidecarEventsResult
	if err := ValidateSidecarSessionEvents(events); err != nil {
		return result, err
	}
	ident := sidecarIdentity{ID: sc.ID, Name: sc.Name, OrgID: sc.OrgID}

	// Grouped by session in order of first appearance. Within a group the
	// sidecar's order holds; across groups it does not matter.
	var order []string
	groups := map[string][]daemon.SessionEvent{}
	for _, e := range events {
		sid := string(e.Event.SessionID)
		if _, ok := groups[sid]; !ok {
			order = append(order, sid)
		}
		groups[sid] = append(groups[sid], e)
	}

	var refused, failed []error
	for _, sid := range order {
		var plan sidecarSessionPlan
		err := db.Transaction(func(tx *gorm.DB) error {
			id := SidecarSessionID(ident.ID, sid)
			if err := models.LockSidecarSession(tx, id); err != nil {
				return err
			}
			prior, err := models.GetSidecarSessionState(tx, ident.OrgID, id)
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				prior = nil
			case err != nil:
				return err
			}
			plan, err = planSidecarSession(ident, sid, prior, groups[sid])
			if err != nil {
				return err
			}
			return applySidecarSessionPlan(tx, ident.OrgID, plan)
		})
		var permanent SidecarEventsRefused
		switch {
		case errors.As(err, &permanent):
			refused = append(refused, permanent)
		case err != nil:
			log.With("sidecar", sc.ID, "session", sid).Warnf("failed recording sidecar session events, reason=%v", err)
			failed = append(failed, err)
		default:
			result.Accepted += plan.Accepted
			result.Duplicates += plan.Duplicates
		}
	}
	if len(failed) > 0 {
		return result, errors.Join(failed...)
	}
	if len(refused) > 0 {
		return result, SidecarEventsRefused{Reason: errors.Join(refused...).Error()}
	}
	return result, nil
}

// applySidecarSessionPlan writes a plan inside tx.
func applySidecarSessionPlan(tx *gorm.DB, orgID string, plan sidecarSessionPlan) error {
	if plan.Accepted == 0 {
		return nil
	}
	if plan.Create != nil {
		if err := models.UpsertSessionTx(tx, *plan.Create); err != nil {
			return fmt.Errorf("creating the session: %w", err)
		}
		// No format: the entries are statement text, and wire-proto would
		// send the query list through the Postgres frame parser.
		if err := models.CreateEmptySessionStreamBlobTx(tx, orgID, plan.SessionID, nil); err != nil {
			return fmt.Errorf("creating the session stream: %w", err)
		}
	}
	if len(plan.Entries) > 0 {
		if err := models.AppendSessionStreamTx(tx, orgID, plan.SessionID, plan.Entries); err != nil {
			return fmt.Errorf("appending to the session stream: %w", err)
		}
	}
	if len(plan.GuardRails) > 0 {
		info, err := json.Marshal(plan.GuardRails)
		if err != nil {
			return fmt.Errorf("encoding guardrails info: %w", err)
		}
		if err := models.UpdateSessionGuardRailsInfoTx(tx, orgID, plan.SessionID, info); err != nil {
			return fmt.Errorf("recording guardrails info: %w", err)
		}
	}
	if err := models.SetSidecarSessionProgress(tx, orgID, plan.SessionID, plan.Sidecar, plan.Metrics); err != nil {
		return fmt.Errorf("recording the session progress: %w", err)
	}
	if len(plan.Masked) > 0 {
		if err := models.IncrementSessionMaskedMetrics(tx, plan.SessionID, plan.Masked); err != nil {
			return fmt.Errorf("recording masking metrics: %w", err)
		}
	}
	if plan.Done != nil {
		if err := models.MarkSessionDoneTx(tx, *plan.Done); err != nil {
			return fmt.Errorf("ending the session: %w", err)
		}
		if err := models.SetSessionMetricsEndedAt(tx, plan.SessionID); err != nil {
			return fmt.Errorf("ending the session metrics: %w", err)
		}
	}
	return nil
}
