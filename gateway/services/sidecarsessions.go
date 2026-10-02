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
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/session/eventbroker"
	eventlogv1 "github.com/hoophq/hoop/gateway/session/eventlog/v1"
	sessionwal "github.com/hoophq/hoop/gateway/session/wal"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// SidecarSessionEventsFlag turns on POST /api/sidecars/events and the
// handshake header that tells a sidecar to use it.
const SidecarSessionEventsFlag = "experimental.sidecar_session_events"

// sidecarGuardRailRuleType: the event names the rule, not its type; a known
// type would make the session page promise matched words.
const sidecarGuardRailRuleType = "sidecar"

// Column limits. A longer value fails every resend: a principal is cut, a
// connection name is refused.
const (
	maxSidecarSessionIDBytes  = 256
	maxSessionConnectionChars = 128 // sessions.connection VARCHAR(128)
	maxSessionUserChars       = 255 // sessions.user_name, user_email VARCHAR(255)
)

// maxSidecarGuardRails caps guardrails_info per session. The stream keeps every
// denial; metadata.sidecar.guardrails_omitted counts the rest.
const maxSidecarGuardRails = 100

// maxSidecarSessionStreamBytes caps the stream as the audit plugin caps its WAL
// read; metrics.truncated marks it.
const maxSidecarSessionStreamBytes = sessionwal.DefaultMaxRead

// Stream entry types, as the audit plugin writes them.
var (
	streamInput  = string(eventlogv1.InputType)
	streamOutput = string(eventlogv1.OutputType)
	streamError  = string(eventlogv1.ErrorType)
)

// SidecarSessionID derives the session id from the authenticated sidecar, so a
// resend lands on the same row and a token writes only its own sessions.
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

// sidecarMirrorConnection names a listener's mirror connection and its type.
// Dia 0 owns these rules; replace this copy with its function after it merges.
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
// apply. The handler answers 4xx, so the sidecar does not resend it.
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

// sidecarSessionPlan is the writes one session's events turn into, applied in
// field order by applySidecarSessionPlan.
type sidecarSessionPlan struct {
	SessionID string
	// Create is set when the row does not exist; any first event creates it.
	Create *models.Session
	// Entries is a JSON array of [elapsed, type, base64] to append.
	Entries json.RawMessage
	// GuardRails are appended to guardrails_info.
	GuardRails []models.SessionGuardRailsInfo
	// Masked adds to private.session_metrics, per info type.
	Masked map[string]int64
	// Metrics replaces the metrics column. Nil when nothing applied.
	Metrics map[string]any
	// Sidecar is merged into metadata.sidecar; it always carries last_seq.
	Sidecar map[string]any
	// User replaces an unknown principal on an existing row.
	User *sidecarUser
	// Live feeds the open session page on this replica, after commit.
	Live []eventbroker.Event
	// Done ends the session.
	Done *models.SessionDone

	Accepted   int
	Duplicates int
}

// planSidecarSession turns one session's events into writes; it does no I/O.
// prior is the row, nil when absent. The PR description maps each kind.
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

	// A pgwire session_start is anonymous: it is written before the startup
	// packet. The first known principal files the session.
	principal := ""
	for _, e := range fresh {
		if knownPrincipal(e.Event.Principal) {
			principal = e.Event.Principal
			break
		}
	}

	var startedAt time.Time
	metrics := map[string]any{}
	plan.Sidecar = map[string]any{}
	if prior != nil {
		startedAt = prior.CreatedAt
		if prior.Metrics != nil {
			metrics = maps.Clone(prior.Metrics)
		}
		if principal != "" && !knownPrincipal(prior.Principal) {
			user := sidecarUserFor(principal)
			plan.User = &user
			if user.Full != "" {
				plan.Sidecar["principal"] = user.Full
			}
		}
	} else {
		if principal == "" {
			principal = fresh[0].Event.Principal
		}
		sess, err := newSidecarSession(sc, plan.SessionID, sessionID, fresh[0].Event, principal)
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
	guardRailsRoom, omitted := maxSidecarGuardRails, int64(0)
	if prior != nil {
		guardRailsRoom -= prior.GuardRails
		omitted = prior.GuardRailsOmitted
	}
	for _, e := range fresh {
		ev := e.Event
		switch ev.Kind {
		case audit.KindStatement:
			stream.add(ev.Timestamp, statementEntryType(ev), ev.Statement)
		case audit.KindViolation:
			stream.add(ev.Timestamp, statementEntryType(ev), ev.Statement)
			// 1µs later: the raw view keys rows by elapsed time.
			stream.add(ev.Timestamp.Add(time.Microsecond), streamError, denialText(ev))
			if len(plan.GuardRails) < guardRailsRoom {
				plan.GuardRails = append(plan.GuardRails, sidecarGuardRail(ev))
			} else {
				omitted++
				plan.Sidecar["guardrails_omitted"] = omitted
			}
		case audit.KindError:
			stream.add(ev.Timestamp, streamError, ev.Error)
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
				Status:     string(openapi.SessionStatusDone),
				EndSession: &end,
				// Not nil: MarkSessionDone merges it, and nil merges into NULL.
				Metrics: map[string]any{},
			}
			plan.Sidecar["statement_count"] = ev.StatementCount
			plan.Sidecar["denied_count"] = ev.DeniedCount
		}
	}
	plan.Sidecar["last_seq"] = lastSeq

	if len(stream.entries) > 0 {
		plan.Entries = json.RawMessage("[" + strings.Join(stream.entries, ",") + "]")
		plan.Live = stream.live
	}
	metrics["event_size"] = stream.size
	metrics["truncated"] = stream.truncated
	if len(plan.Masked) > 0 {
		addDataMasking(metrics, plan.Masked)
	}
	plan.Metrics = metrics
	return plan, nil
}

// statementEntryType: a server statement (a CommandComplete tag reads
// "SELECT 1") is output, not a query.
func statementEntryType(ev audit.Event) string {
	if ev.Direction == inspect.FromServer {
		return streamOutput
	}
	return streamInput
}

// newSidecarSession builds the row from the first event received for it.
func newSidecarSession(sc sidecarIdentity, id, sidecarSessionID string, ev audit.Event, principal string) (*models.Session, error) {
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
	// Always raw: the derived format reads custom/* as a terminal (pty).
	format := pb.RecordingFormatRaw
	sess := &models.Session{
		ID:                id,
		OrgID:             sc.OrgID,
		Connection:        mirror.Name,
		ConnectionType:    mirror.Type,
		ConnectionSubtype: mirror.Subtype,
		Verb:              pb.ClientVerbConnect,
		RecordingFormat:   &format,
		Status:            string(openapi.SessionStatusOpen),
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
	user := sidecarUserFor(principal)
	sess.UserName, sess.UserEmail = user.Name, user.Email
	if user.Full != "" {
		sess.Metadata["sidecar"].(map[string]any)["principal"] = user.Full
	}
	return sess, nil
}

// sidecarUser is a principal as the session row files it.
type sidecarUser struct {
	Name  string
	Email string
	// Full is the whole principal when the column cut it.
	Full string
}

// sidecarUserFor files a principal: an email as user_email, anything else
// (a database role) as user_name.
func sidecarUserFor(principal string) sidecarUser {
	var out sidecarUser
	p := pgText(principal)
	if short := truncateChars(p, maxSessionUserChars); short != p {
		out.Full = p
		p = short
	}
	if strings.Contains(p, "@") {
		out.Email = p
	} else {
		out.Name = p
	}
	return out
}

// knownPrincipal reports a resolved principal, not the anonymous placeholder.
func knownPrincipal(p string) bool {
	p = pgText(p)
	return p != "" && p != session.AnonymousPrincipal
}

// pgText drops NUL, which Postgres refuses in text and jsonb on every resend.
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

// sidecarStream builds the entries a batch appends, in the audit plugin's
// format: [seconds since start, type, base64].
type sidecarStream struct {
	startedAt time.Time
	entries   []string
	live      []eventbroker.Event
	size      int64
	truncated bool
}

func (s *sidecarStream) add(at time.Time, kind, text string) {
	if text == "" || s.truncated {
		return
	}
	// A clock step on the sidecar is not a negative duration.
	elapsed := max(at.Sub(s.startedAt).Seconds(), 0)
	entry, err := json.Marshal([]any{elapsed, kind, base64.StdEncoding.EncodeToString([]byte(text))})
	if err != nil {
		return
	}
	s.entries = append(s.entries, string(entry))
	s.live = append(s.live, eventbroker.Event{Time: at, Type: kind, Payload: []byte(text)})
	s.size += int64(len(entry))
	if s.size >= maxSidecarSessionStreamBytes {
		s.truncated = true
	}
}

// denialText is the error entry after a denied statement.
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
	direction := "input" // guardrails_info has no constant for these

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

// maskedInfoType joins the entities: the event counts cells across all of
// them, and splitting the count would invent numbers.
func maskedInfoType(entities []string) string {
	if len(entities) == 0 {
		return "unknown"
	}
	sorted := make([]string, len(entities))
	for i, e := range entities {
		sorted[i] = pgText(e)
	}
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

// metricInt reads a number from decoded JSON (float64) or from this package
// (int64).
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

// ApplySidecarSessionEvents applies each session in its own transaction. It
// returns SidecarEventsRefused when every failure is permanent.
func ApplySidecarSessionEvents(db *gorm.DB, sc *models.Sidecar, events []daemon.SessionEvent) (SidecarEventsResult, error) {
	var result SidecarEventsResult
	if err := ValidateSidecarSessionEvents(events); err != nil {
		return result, err
	}
	ident := sidecarIdentity{ID: sc.ID, Name: sc.Name, OrgID: sc.OrgID}

	// Grouped by session; the sidecar's order holds within a group.
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
		case permanentDBError(err):
			log.With("sidecar", sc.ID, "session", sid).Warnf("refused sidecar session events the database cannot hold, reason=%v", err)
			refused = append(refused, SidecarEventsRefused{Reason: fmt.Sprintf("session %s: %v", sid, err)})
		case err != nil:
			log.With("sidecar", sc.ID, "session", sid).Warnf("failed recording sidecar session events, reason=%v", err)
			failed = append(failed, err)
		default:
			result.Accepted += plan.Accepted
			result.Duplicates += plan.Duplicates
			publishSidecarSession(plan)
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

// publishSidecarSession feeds the live session page on this replica, as the
// audit plugin does; the end closes the page's stream.
func publishSidecarSession(plan sidecarSessionPlan) {
	for _, ev := range plan.Live {
		eventbroker.Default.Publish(plan.SessionID, ev)
	}
	if plan.Done != nil {
		eventbroker.Default.Remove(plan.SessionID)
	}
}

// permanentDBError reports an error the batch causes, which every resend
// repeats: a 500 would make the sidecar resend it forever.
func permanentDBError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && len(pgErr.Code) >= 2 {
		switch pgErr.Code[:2] {
		case "22", // data exception
			"23", // integrity constraint violation
			"54": // program limit exceeded
			return true
		}
	}
	// TranslateError replaces some class 23 codes with gorm errors.
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		errors.Is(err, gorm.ErrForeignKeyViolated) ||
		errors.Is(err, gorm.ErrCheckConstraintViolated)
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
		// No format: the entries are text; wire-proto would parse them as frames.
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
	if plan.User != nil {
		if err := models.SetSidecarSessionUser(tx, orgID, plan.SessionID, plan.User.Name, plan.User.Email); err != nil {
			return fmt.Errorf("recording the principal: %w", err)
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
