package services

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	sidecarsession "github.com/hoophq/hoop/sidecar/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testSidecarIdent = sidecarIdentity{ID: "8f0c7a52-3d43-4a4e-9a55-0b8a6f1f4c11", Name: "edge", OrgID: "org-1"}
	testSidecarT0    = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
)

const testSidecarSessionID = "sess-1"

// sidecarEvent is one event of testSidecarSessionID on a postgres listener
// "pg", at t0 plus offset.
func sidecarEvent(seq int64, kind audit.Kind, offset time.Duration, mutate ...func(*audit.Event)) daemon.SessionEvent {
	ev := audit.Event{
		Kind:       kind,
		Timestamp:  testSidecarT0.Add(offset),
		SessionID:  testSidecarSessionID,
		Principal:  "alice@example.com",
		Protocol:   inspect.Postgres,
		Connection: "pg",
	}
	for _, m := range mutate {
		m(&ev)
	}
	return daemon.SessionEvent{Seq: seq, Event: ev}
}

// existingSidecarSession is the state of a session the gateway already holds.
func existingSidecarSession(lastSeq int64) *models.SidecarSessionState {
	return &models.SidecarSessionState{CreatedAt: testSidecarT0, LastSeq: lastSeq, Principal: "alice@example.com"}
}

// decodedMetrics decodes raw like GetSidecarSessionState does, so numbers are
// float64.
func decodedMetrics(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &m))
	return m
}

type sidecarStreamEntry struct {
	Elapsed float64
	Type    string
	Text    string
	// Size is the byte length of the encoded entry.
	Size int64
}

// decodeSidecarEntries decodes plan.Entries and asserts each entry is
// [number, string, base64 string].
func decodeSidecarEntries(t *testing.T, raw json.RawMessage) []sidecarStreamEntry {
	t.Helper()
	if raw == nil {
		return nil
	}
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &items), "entries are not a JSON array: %s", raw)
	out := make([]sidecarStreamEntry, 0, len(items))
	for i, item := range items {
		var fields []any
		require.NoError(t, json.Unmarshal(item, &fields), "entry %d is not an array: %s", i, item)
		require.Len(t, fields, 3, "entry %d: %s", i, item)
		elapsed, ok := fields[0].(float64)
		require.True(t, ok, "entry %d: elapsed is %T, want a number", i, fields[0])
		kind, ok := fields[1].(string)
		require.True(t, ok, "entry %d: type is %T, want a string", i, fields[1])
		b64, ok := fields[2].(string)
		require.True(t, ok, "entry %d: payload is %T, want a string", i, fields[2])
		text, err := base64.StdEncoding.DecodeString(b64)
		require.NoError(t, err, "entry %d: payload is not base64", i)
		out = append(out, sidecarStreamEntry{Elapsed: elapsed, Type: kind, Text: string(text), Size: int64(len(item))})
	}
	return out
}

func entriesSize(entries []sidecarStreamEntry) int64 {
	var n int64
	for _, e := range entries {
		n += e.Size
	}
	return n
}

func TestPlanSidecarSessionKinds(t *testing.T) {
	type want struct {
		entries    []sidecarStreamEntry // Size is not compared
		guardRails []models.SessionGuardRailsInfo
		masked     map[string]int64
		dataMask   map[string]any // metrics.data_masking; nil means absent
		done       bool
		sidecar    map[string]any
	}
	for _, tt := range []struct {
		name  string
		event daemon.SessionEvent
		want  want
	}{
		{
			name:  "statement",
			event: sidecarEvent(2, audit.KindStatement, 2500*time.Millisecond, func(e *audit.Event) { e.Statement = "SELECT 1" }),
			want: want{
				entries: []sidecarStreamEntry{{Elapsed: 2.5, Type: "i", Text: "SELECT 1"}},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			// A Postgres CommandComplete tag reads like a query. It is output.
			name: "statement from the server",
			event: sidecarEvent(2, audit.KindStatement, time.Second, func(e *audit.Event) {
				e.Statement = "SELECT 1"
				e.Direction = inspect.FromServer
			}),
			want: want{
				entries: []sidecarStreamEntry{{Elapsed: 1, Type: "o", Text: "SELECT 1"}},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			name:  "statement without text",
			event: sidecarEvent(2, audit.KindStatement, time.Second),
			want:  want{sidecar: map[string]any{"last_seq": int64(2)}},
		},
		{
			name: "violation with rule and message",
			event: sidecarEvent(2, audit.KindViolation, 3*time.Second, func(e *audit.Event) {
				e.Statement = "DROP TABLE users"
				e.Rule = "no-drop"
				e.Message = "drop is not allowed"
				e.Direction = inspect.FromClient
			}),
			want: want{
				entries: []sidecarStreamEntry{
					{Elapsed: 3, Type: "i", Text: "DROP TABLE users"},
					{Elapsed: 3, Type: "e", Text: `denied by rule "no-drop": drop is not allowed`},
				},
				guardRails: []models.SessionGuardRailsInfo{{
					RuleName:     "no-drop",
					Rule:         models.SessionGuardRailMatchedRule{Type: "sidecar"},
					Direction:    "input",
					MatchedWords: []string{},
					Message:      "drop is not allowed",
				}},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			name: "violation from the server, rule only",
			event: sidecarEvent(2, audit.KindViolation, time.Second, func(e *audit.Event) {
				e.Statement = "SELECT ssn FROM users"
				e.Rule = "no-ssn"
				e.Direction = inspect.FromServer
			}),
			want: want{
				entries: []sidecarStreamEntry{
					{Elapsed: 1, Type: "o", Text: "SELECT ssn FROM users"},
					{Elapsed: 1, Type: "e", Text: `denied by rule "no-ssn"`},
				},
				guardRails: []models.SessionGuardRailsInfo{{
					RuleName:     "no-ssn",
					Rule:         models.SessionGuardRailMatchedRule{Type: "sidecar"},
					Direction:    "output",
					MatchedWords: []string{},
				}},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			name: "violation, message only",
			event: sidecarEvent(2, audit.KindViolation, time.Second, func(e *audit.Event) {
				e.Statement = "DELETE FROM t"
				e.Message = "ask a DBA"
			}),
			want: want{
				entries: []sidecarStreamEntry{
					{Elapsed: 1, Type: "i", Text: "DELETE FROM t"},
					{Elapsed: 1, Type: "e", Text: "denied: ask a DBA"},
				},
				guardRails: []models.SessionGuardRailsInfo{{
					Rule:         models.SessionGuardRailMatchedRule{Type: "sidecar"},
					Direction:    "input",
					MatchedWords: []string{},
					Message:      "ask a DBA",
				}},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			name: "violation, no rule and no message",
			event: sidecarEvent(2, audit.KindViolation, time.Second, func(e *audit.Event) {
				e.Statement = "TRUNCATE t"
			}),
			want: want{
				entries: []sidecarStreamEntry{
					{Elapsed: 1, Type: "i", Text: "TRUNCATE t"},
					{Elapsed: 1, Type: "e", Text: "denied"},
				},
				guardRails: []models.SessionGuardRailsInfo{{
					Rule:         models.SessionGuardRailMatchedRule{Type: "sidecar"},
					Direction:    "input",
					MatchedWords: []string{},
				}},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			name:  "error",
			event: sidecarEvent(2, audit.KindError, 4*time.Second, func(e *audit.Event) { e.Error = "upstream reset" }),
			want: want{
				entries: []sidecarStreamEntry{{Elapsed: 4, Type: "e", Text: "upstream reset"}},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			name: "masked, one entity",
			event: sidecarEvent(2, audit.KindMasked, time.Second, func(e *audit.Event) {
				e.MaskedEntities = []string{"email"}
				e.MaskedCount = 3
			}),
			want: want{
				masked: map[string]int64{"email": 3},
				dataMask: map[string]any{
					"transformed_bytes":  int64(0),
					"err_count":          int64(0),
					"info_types":         map[string]any{"email": int64(3)},
					"total_redact_count": int64(3),
				},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			name: "masked, several entities filed under one sorted key",
			event: sidecarEvent(2, audit.KindMasked, time.Second, func(e *audit.Event) {
				e.MaskedEntities = []string{"ssn", "email", "ssn"}
				e.MaskedCount = 5
			}),
			want: want{
				masked: map[string]int64{"email+ssn": 5},
				dataMask: map[string]any{
					"transformed_bytes":  int64(0),
					"err_count":          int64(0),
					"info_types":         map[string]any{"email+ssn": int64(5)},
					"total_redact_count": int64(5),
				},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			name:  "masked, no entity named",
			event: sidecarEvent(2, audit.KindMasked, time.Second, func(e *audit.Event) { e.MaskedCount = 1 }),
			want: want{
				masked: map[string]int64{"unknown": 1},
				dataMask: map[string]any{
					"transformed_bytes":  int64(0),
					"err_count":          int64(0),
					"info_types":         map[string]any{"unknown": int64(1)},
					"total_redact_count": int64(1),
				},
				sidecar: map[string]any{"last_seq": int64(2)},
			},
		},
		{
			name: "masked, zero count",
			event: sidecarEvent(2, audit.KindMasked, time.Second, func(e *audit.Event) {
				e.MaskedEntities = []string{"email"}
			}),
			want: want{sidecar: map[string]any{"last_seq": int64(2)}},
		},
		{
			name: "session_end",
			event: sidecarEvent(2, audit.KindSessionEnd, time.Minute, func(e *audit.Event) {
				e.StatementCount = 7
				e.DeniedCount = 2
			}),
			want: want{
				done:    true,
				sidecar: map[string]any{"last_seq": int64(2), "statement_count": 7, "denied_count": 2},
			},
		},
		{
			name: "activity",
			event: sidecarEvent(2, audit.KindActivity, time.Second, func(e *audit.Event) {
				e.Metadata = map[string]string{audit.MetadataActivity: "forward_open"}
			}),
			want: want{sidecar: map[string]any{"last_seq": int64(2)}},
		},
		{
			name:  "unknown kind",
			event: sidecarEvent(2, audit.Kind("from_a_newer_sidecar"), time.Second, func(e *audit.Event) { e.Statement = "x" }),
			want:  want{sidecar: map[string]any{"last_seq": int64(2)}},
		},
		{
			name:  "session_start on an existing session",
			event: sidecarEvent(2, audit.KindSessionStart, 0),
			want:  want{sidecar: map[string]any{"last_seq": int64(2)}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, existingSidecarSession(1),
				[]daemon.SessionEvent{tt.event})
			require.NoError(t, err)

			assert.Equal(t, SidecarSessionID(testSidecarIdent.ID, testSidecarSessionID), plan.SessionID)
			assert.Nil(t, plan.Create, "an existing session is not created again")
			assert.Equal(t, 1, plan.Accepted)
			assert.Equal(t, 0, plan.Duplicates)

			got := decodeSidecarEntries(t, plan.Entries)
			require.Len(t, got, len(tt.want.entries), "entries: %s", plan.Entries)
			for i, w := range tt.want.entries {
				assert.Equal(t, w.Elapsed, got[i].Elapsed, "entry %d elapsed", i)
				assert.Equal(t, w.Type, got[i].Type, "entry %d type", i)
				assert.Equal(t, w.Text, got[i].Text, "entry %d text", i)
			}
			if len(tt.want.entries) == 0 {
				assert.Nil(t, plan.Entries)
			}

			assert.Equal(t, tt.want.guardRails, plan.GuardRails)
			assert.Equal(t, tt.want.masked, plan.Masked)
			assert.Equal(t, tt.want.sidecar, plan.Sidecar)

			require.NotNil(t, plan.Metrics)
			assert.Equal(t, entriesSize(got), plan.Metrics["event_size"])
			assert.Equal(t, false, plan.Metrics["truncated"])
			if tt.want.dataMask == nil {
				assert.NotContains(t, plan.Metrics, "data_masking")
			} else {
				assert.Equal(t, tt.want.dataMask, plan.Metrics["data_masking"])
			}

			if !tt.want.done {
				assert.Nil(t, plan.Done)
				return
			}
			require.NotNil(t, plan.Done)
			end := testSidecarT0.Add(time.Minute)
			assert.Equal(t, models.SessionDone{
				ID:         plan.SessionID,
				OrgID:      testSidecarIdent.OrgID,
				Status:     "done",
				EndSession: &end,
				Metrics:    map[string]any{},
			}, *plan.Done)
		})
	}
}

func TestPlanSidecarSessionStart(t *testing.T) {
	plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, nil,
		[]daemon.SessionEvent{sidecarEvent(1, audit.KindSessionStart, 0)})
	require.NoError(t, err)

	id := SidecarSessionID(testSidecarIdent.ID, testSidecarSessionID)
	assert.Equal(t, id, plan.SessionID)
	assert.Equal(t, 1, plan.Accepted)
	assert.Nil(t, plan.Entries)
	assert.Nil(t, plan.Done)
	assert.Equal(t, map[string]any{"last_seq": int64(1)}, plan.Sidecar)
	assert.Equal(t, map[string]any{"event_size": int64(0), "truncated": false}, plan.Metrics)

	require.NotNil(t, plan.Create)
	format := pb.RecordingFormatRaw
	assert.Equal(t, &models.Session{
		ID:                id,
		OrgID:             testSidecarIdent.OrgID,
		Connection:        "edge-pg",
		ConnectionType:    "database",
		ConnectionSubtype: "postgres",
		Verb:              pb.ClientVerbConnect,
		RecordingFormat:   &format,
		Status:            "open",
		IdentityType:      plugintypes.IdentityTypeSidecar,
		Origin:            pb.SessionOriginSidecar,
		UserEmail:         "alice@example.com",
		CreatedAt:         testSidecarT0,
		Metadata: map[string]any{
			"sidecar": map[string]any{
				"id":         testSidecarIdent.ID,
				"name":       "edge",
				"listener":   "pg",
				"session_id": testSidecarSessionID,
				"last_seq":   0,
			},
		},
	}, plan.Create)
	assert.Equal(t, "sidecar", plan.Create.IdentityType)
	assert.Equal(t, "sidecar", plan.Create.Origin)
	assert.Equal(t, "raw", *plan.Create.RecordingFormat)
}

func TestPlanSidecarSessionCreatedFromLaterEvent(t *testing.T) {
	// The gateway missed session_start: the first event it gets creates the
	// session and starts its clock.
	events := []daemon.SessionEvent{
		sidecarEvent(4, audit.KindStatement, 5*time.Second, func(e *audit.Event) { e.Statement = "SELECT 1" }),
		sidecarEvent(5, audit.KindStatement, 7*time.Second, func(e *audit.Event) { e.Statement = "SELECT 2" }),
		// A clock step back is not a negative elapsed.
		sidecarEvent(6, audit.KindError, 4*time.Second, func(e *audit.Event) { e.Error = "boom" }),
	}
	plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, nil, events)
	require.NoError(t, err)

	require.NotNil(t, plan.Create)
	assert.Equal(t, testSidecarT0.Add(5*time.Second), plan.Create.CreatedAt)
	assert.Equal(t, "open", plan.Create.Status)
	assert.Equal(t, "edge-pg", plan.Create.Connection)
	assert.Equal(t, 3, plan.Accepted)
	assert.Equal(t, map[string]any{"last_seq": int64(6)}, plan.Sidecar)

	got := decodeSidecarEntries(t, plan.Entries)
	require.Len(t, got, 3)
	assert.Equal(t, sidecarStreamEntry{Elapsed: 0, Type: "i", Text: "SELECT 1", Size: got[0].Size}, got[0])
	assert.Equal(t, sidecarStreamEntry{Elapsed: 2, Type: "i", Text: "SELECT 2", Size: got[1].Size}, got[1])
	assert.Equal(t, sidecarStreamEntry{Elapsed: 0, Type: "e", Text: "boom", Size: got[2].Size}, got[2])
	assert.Equal(t, entriesSize(got), plan.Metrics["event_size"])

	t.Run("from session_end", func(t *testing.T) {
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, nil, []daemon.SessionEvent{
			sidecarEvent(9, audit.KindSessionEnd, time.Minute, func(e *audit.Event) { e.StatementCount = 3 }),
		})
		require.NoError(t, err)
		require.NotNil(t, plan.Create)
		require.NotNil(t, plan.Done)
		assert.Equal(t, testSidecarT0.Add(time.Minute), plan.Create.CreatedAt)
		assert.Equal(t, testSidecarT0.Add(time.Minute), *plan.Done.EndSession)
		assert.Equal(t, map[string]any{"last_seq": int64(9), "statement_count": 3, "denied_count": 0}, plan.Sidecar)
	})
}

func TestPlanSidecarSessionDedup(t *testing.T) {
	stmt := func(seq int64) daemon.SessionEvent {
		return sidecarEvent(seq, audit.KindStatement, time.Duration(seq)*time.Second,
			func(e *audit.Event) { e.Statement = "q" + string(rune('0'+seq)) })
	}
	for _, tt := range []struct {
		name        string
		prior       *models.SidecarSessionState
		seqs        []int64
		wantTexts   []string
		wantAcc     int
		wantDup     int
		wantLastSeq int64
	}{
		{
			name:        "at or below the prior last_seq",
			prior:       existingSidecarSession(5),
			seqs:        []int64{3, 5, 6, 7},
			wantTexts:   []string{"q6", "q7"},
			wantAcc:     2,
			wantDup:     2,
			wantLastSeq: 7,
		},
		{
			name:        "repeated within one batch",
			prior:       nil,
			seqs:        []int64{1, 2, 2, 3},
			wantTexts:   []string{"q1", "q2", "q3"},
			wantAcc:     3,
			wantDup:     1,
			wantLastSeq: 3,
		},
		{
			name:        "behind the batch's own highest seq",
			prior:       existingSidecarSession(1),
			seqs:        []int64{4, 3},
			wantTexts:   []string{"q4"},
			wantAcc:     1,
			wantDup:     1,
			wantLastSeq: 4,
		},
		{
			name:        "a gap is not a duplicate",
			prior:       existingSidecarSession(1),
			seqs:        []int64{2, 5, 9},
			wantTexts:   []string{"q2", "q5", "q9"},
			wantAcc:     3,
			wantLastSeq: 9,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var events []daemon.SessionEvent
			for _, s := range tt.seqs {
				events = append(events, stmt(s))
			}
			plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, tt.prior, events)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAcc, plan.Accepted)
			assert.Equal(t, tt.wantDup, plan.Duplicates)
			assert.Equal(t, tt.wantLastSeq, plan.Sidecar["last_seq"])
			var texts []string
			for _, e := range decodeSidecarEntries(t, plan.Entries) {
				texts = append(texts, e.Text)
			}
			assert.Equal(t, tt.wantTexts, texts)
		})
	}

	t.Run("a batch that is all resend writes nothing", func(t *testing.T) {
		prior := existingSidecarSession(5)
		prior.Metrics = decodedMetrics(t, `{"event_size": 10, "truncated": false}`)
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, prior, []daemon.SessionEvent{
			stmt(4), stmt(5),
			sidecarEvent(5, audit.KindSessionEnd, time.Minute),
			sidecarEvent(3, audit.KindMasked, time.Second, func(e *audit.Event) { e.MaskedCount = 2 }),
			sidecarEvent(2, audit.KindViolation, time.Second, func(e *audit.Event) { e.Statement = "DROP" }),
		})
		require.NoError(t, err)
		assert.Equal(t, sidecarSessionPlan{
			SessionID:  SidecarSessionID(testSidecarIdent.ID, testSidecarSessionID),
			Duplicates: 5,
		}, plan)
	})
}

func TestPlanSidecarSessionMergesPriorMetrics(t *testing.T) {
	prior := existingSidecarSession(3)
	prior.Metrics = decodedMetrics(t, `{
		"event_size": 100,
		"truncated": false,
		"other": "kept",
		"data_masking": {
			"transformed_bytes": 10,
			"err_count": 1,
			"info_types": {"email": 2, "ssn": 1},
			"total_redact_count": 3
		}
	}`)
	priorCopy := decodedMetrics(t, `{
		"event_size": 100,
		"truncated": false,
		"other": "kept",
		"data_masking": {
			"transformed_bytes": 10,
			"err_count": 1,
			"info_types": {"email": 2, "ssn": 1},
			"total_redact_count": 3
		}
	}`)

	plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, prior, []daemon.SessionEvent{
		sidecarEvent(4, audit.KindStatement, time.Second, func(e *audit.Event) { e.Statement = "SELECT email FROM users" }),
		sidecarEvent(5, audit.KindMasked, time.Second, func(e *audit.Event) {
			e.MaskedEntities = []string{"email"}
			e.MaskedCount = 4
		}),
		sidecarEvent(6, audit.KindMasked, time.Second, func(e *audit.Event) {
			e.MaskedEntities = []string{"phone"}
			e.MaskedCount = 1
		}),
		sidecarEvent(7, audit.KindMasked, time.Second, func(e *audit.Event) {
			e.MaskedEntities = []string{"email"}
			e.MaskedCount = 2
		}),
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]int64{"email": 6, "phone": 1}, plan.Masked,
		"session_metrics gets this batch's counts only")

	entries := decodeSidecarEntries(t, plan.Entries)
	require.Len(t, entries, 1)
	assert.Equal(t, 100+entriesSize(entries), plan.Metrics["event_size"])
	assert.Equal(t, false, plan.Metrics["truncated"])
	assert.Equal(t, "kept", plan.Metrics["other"])
	assert.Equal(t, map[string]any{
		"transformed_bytes": float64(10),
		"err_count":         float64(1),
		"info_types": map[string]any{
			"email": int64(8),
			"ssn":   float64(1),
			"phone": int64(1),
		},
		"total_redact_count": int64(10),
	}, plan.Metrics["data_masking"])

	// The JSON the column gets holds the merged numbers.
	raw, err := json.Marshal(plan.Metrics)
	require.NoError(t, err)
	reread := decodedMetrics(t, string(raw))
	dm := reread["data_masking"].(map[string]any)
	assert.Equal(t, map[string]any{"email": float64(8), "ssn": float64(1), "phone": float64(1)}, dm["info_types"])
	assert.Equal(t, float64(10), dm["total_redact_count"])

	assert.Equal(t, priorCopy, prior.Metrics, "the prior state is not changed")
}

func TestPlanSidecarSessionStreamTruncation(t *testing.T) {
	stmt := func(seq int64, text string) daemon.SessionEvent {
		return sidecarEvent(seq, audit.KindStatement, time.Second, func(e *audit.Event) { e.Statement = text })
	}

	t.Run("the entry that crosses the cap is kept, later ones are not", func(t *testing.T) {
		prior := existingSidecarSession(1)
		prior.Metrics = decodedMetrics(t,
			`{"event_size": `+jsonInt(maxSidecarSessionStreamBytes-1)+`, "truncated": false}`)
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, prior, []daemon.SessionEvent{
			stmt(2, "SELECT 1"),
			stmt(3, "SELECT 2"),
			sidecarEvent(4, audit.KindError, time.Second, func(e *audit.Event) { e.Error = "late" }),
		})
		require.NoError(t, err)
		assert.Equal(t, 3, plan.Accepted)
		assert.Equal(t, int64(4), plan.Sidecar["last_seq"], "dropped entries still advance last_seq")

		entries := decodeSidecarEntries(t, plan.Entries)
		require.Len(t, entries, 1)
		assert.Equal(t, "SELECT 1", entries[0].Text)
		assert.Equal(t, int64(maxSidecarSessionStreamBytes-1)+entries[0].Size, plan.Metrics["event_size"])
		assert.Equal(t, true, plan.Metrics["truncated"])
	})

	t.Run("a truncated session takes no more entries", func(t *testing.T) {
		prior := existingSidecarSession(1)
		prior.Metrics = decodedMetrics(t,
			`{"event_size": `+jsonInt(maxSidecarSessionStreamBytes+20)+`, "truncated": true}`)
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, prior, []daemon.SessionEvent{
			stmt(2, "SELECT 1"),
			sidecarEvent(3, audit.KindViolation, time.Second, func(e *audit.Event) {
				e.Statement = "DROP TABLE t"
				e.Rule = "no-drop"
			}),
			sidecarEvent(4, audit.KindMasked, time.Second, func(e *audit.Event) { e.MaskedCount = 1 }),
		})
		require.NoError(t, err)
		assert.Nil(t, plan.Entries)
		assert.Equal(t, int64(maxSidecarSessionStreamBytes+20), plan.Metrics["event_size"])
		assert.Equal(t, true, plan.Metrics["truncated"])
		// The stream is full; the rest of the record is not.
		assert.Len(t, plan.GuardRails, 1)
		assert.Equal(t, map[string]int64{"unknown": 1}, plan.Masked)
		assert.Equal(t, int64(4), plan.Sidecar["last_seq"])
	})

	t.Run("under the cap nothing is truncated", func(t *testing.T) {
		prior := existingSidecarSession(1)
		prior.Metrics = decodedMetrics(t, `{"event_size": 0, "truncated": false}`)
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, prior, []daemon.SessionEvent{
			stmt(2, "SELECT 1"), stmt(3, "SELECT 2"),
		})
		require.NoError(t, err)
		entries := decodeSidecarEntries(t, plan.Entries)
		assert.Len(t, entries, 2)
		assert.Equal(t, false, plan.Metrics["truncated"])
	})
}

func jsonInt(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestPlanSidecarSessionPrincipal(t *testing.T) {
	longName := strings.Repeat("a", 300)
	longRunes := strings.Repeat("é", 300)
	for _, tt := range []struct {
		name          string
		principal     string
		wantEmail     string
		wantName      string
		wantPrincipal string // metadata.sidecar.principal; empty means absent
	}{
		{name: "email", principal: "alice@example.com", wantEmail: "alice@example.com"},
		{name: "database role", principal: "app_rw", wantName: "app_rw"},
		{name: "empty", principal: ""},
		{name: "exactly 255 characters", principal: strings.Repeat("b", 255), wantName: strings.Repeat("b", 255)},
		{
			name:          "longer than 255 characters",
			principal:     longName,
			wantName:      longName[:255],
			wantPrincipal: longName,
		},
		{
			name:          "longer than 255 characters, counted in runes",
			principal:     longRunes,
			wantName:      strings.Repeat("é", 255),
			wantPrincipal: longRunes,
		},
		{name: "NUL bytes are dropped", principal: "al\x00ice@exam\x00ple.com", wantEmail: "alice@example.com"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, nil, []daemon.SessionEvent{
				sidecarEvent(1, audit.KindSessionStart, 0, func(e *audit.Event) { e.Principal = tt.principal }),
			})
			require.NoError(t, err)
			require.NotNil(t, plan.Create)
			assert.Equal(t, tt.wantEmail, plan.Create.UserEmail)
			assert.Equal(t, tt.wantName, plan.Create.UserName)
			assert.LessOrEqual(t, utf8.RuneCountInString(plan.Create.UserName), maxSessionUserChars)

			md := plan.Create.Metadata["sidecar"].(map[string]any)
			if tt.wantPrincipal == "" {
				assert.NotContains(t, md, "principal")
				return
			}
			assert.Equal(t, tt.wantPrincipal, md["principal"])
		})
	}
}

func TestPlanSidecarSessionStripsNUL(t *testing.T) {
	const rawSessionID = "sess\x00-2"
	events := []daemon.SessionEvent{
		{Seq: 1, Event: audit.Event{
			Kind:       audit.KindSessionStart,
			Timestamp:  testSidecarT0,
			SessionID:  sidecarsession.ID(rawSessionID),
			Principal:  "app\x00_rw",
			Protocol:   inspect.Postgres,
			Connection: "p\x00g",
		}},
		{Seq: 2, Event: audit.Event{
			Kind:       audit.KindViolation,
			Timestamp:  testSidecarT0.Add(time.Second),
			SessionID:  sidecarsession.ID(rawSessionID),
			Protocol:   inspect.Postgres,
			Connection: "p\x00g",
			Statement:  "DROP\x00",
			Rule:       "no\x00-drop",
			Message:    "not\x00 allowed",
		}},
	}
	plan, err := planSidecarSession(testSidecarIdent, rawSessionID, nil, events)
	require.NoError(t, err)

	// The id is derived from the raw value, so a resend lands on the same row.
	assert.Equal(t, SidecarSessionID(testSidecarIdent.ID, rawSessionID), plan.SessionID)
	require.NotNil(t, plan.Create)
	assert.Equal(t, "edge-pg", plan.Create.Connection)
	assert.Equal(t, "app_rw", plan.Create.UserName)
	md := plan.Create.Metadata["sidecar"].(map[string]any)
	assert.Equal(t, "pg", md["listener"])
	assert.Equal(t, "sess-2", md["session_id"])

	require.Len(t, plan.GuardRails, 1)
	assert.Equal(t, "no-drop", plan.GuardRails[0].RuleName)
	assert.Equal(t, "not allowed", plan.GuardRails[0].Message)

	// The stream is base64: a NUL in it reaches no text column.
	entries := decodeSidecarEntries(t, plan.Entries)
	require.Len(t, entries, 2)
	assert.Equal(t, "DROP\x00", entries[0].Text)

	t.Run("masked entity", func(t *testing.T) {
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, existingSidecarSession(1),
			[]daemon.SessionEvent{sidecarEvent(2, audit.KindMasked, time.Second, func(e *audit.Event) {
				e.MaskedEntities = []string{"em\x00ail"}
				e.MaskedCount = 1
			})})
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{"email": 1}, plan.Masked)
		raw, err := json.Marshal(plan.Metrics)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `\u0000`)
	})
}

func TestPlanSidecarSessionRefusals(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*audit.Event)
		wantMsg string
	}{
		{name: "empty listener", mutate: func(e *audit.Event) { e.Connection = "" }, wantMsg: "names no listener"},
		{name: "listener of NUL bytes only", mutate: func(e *audit.Event) { e.Connection = "\x00\x00" }, wantMsg: "names no listener"},
		{name: "unknown protocol", mutate: func(e *audit.Event) { e.Protocol = "redis" }, wantMsg: `protocol "redis" has no connection type`},
		{name: "no protocol", mutate: func(e *audit.Event) { e.Protocol = "" }, wantMsg: `protocol "" has no connection type`},
		{
			name:    "connection name over 128 characters",
			mutate:  func(e *audit.Event) { e.Connection = strings.Repeat("l", 128-len("edge-")+1) },
			wantMsg: "longer than 128 characters",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, nil, []daemon.SessionEvent{
				sidecarEvent(1, audit.KindSessionStart, 0, tt.mutate),
				sidecarEvent(2, audit.KindStatement, time.Second, func(e *audit.Event) { e.Statement = "SELECT 1" }),
			})
			require.Error(t, err)
			var refused SidecarEventsRefused
			require.True(t, errors.As(err, &refused), "want SidecarEventsRefused, got %T: %v", err, err)
			assert.Contains(t, refused.Reason, tt.wantMsg)
			assert.Contains(t, refused.Reason, testSidecarSessionID)
			assert.Nil(t, plan.Create)
		})
	}

	t.Run("connection name of exactly 128 characters", func(t *testing.T) {
		listener := strings.Repeat("l", 128-len("edge-"))
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, nil, []daemon.SessionEvent{
			sidecarEvent(1, audit.KindSessionStart, 0, func(e *audit.Event) { e.Connection = listener }),
		})
		require.NoError(t, err)
		require.NotNil(t, plan.Create)
		assert.Equal(t, "edge-"+listener, plan.Create.Connection)
	})

	t.Run("an existing session does not need a listener", func(t *testing.T) {
		// Only the event that creates the session names the mirror.
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, existingSidecarSession(1), []daemon.SessionEvent{
			sidecarEvent(2, audit.KindStatement, time.Second, func(e *audit.Event) {
				e.Connection = ""
				e.Protocol = "redis"
				e.Statement = "SELECT 1"
			}),
		})
		require.NoError(t, err)
		assert.Nil(t, plan.Create)
		assert.Equal(t, 1, plan.Accepted)
	})
}

func TestSidecarMirrorConnection(t *testing.T) {
	for _, tt := range []struct {
		protocol    string
		wantType    string
		wantSubtype string
	}{
		{"postgres", "database", "postgres"},
		{"mysql", "database", "mysql"},
		{"mssql", "database", "mssql"},
		{"mongodb", "database", "mongodb"},
		{"ssh", "application", "ssh"},
		{"http", "httpproxy", "httpproxy"},
		{"clickhouse", "custom", "clickhouse"},
		{"grpc", "custom", "grpc"},
		{"spanner", "custom", "spanner"},
	} {
		t.Run(tt.protocol, func(t *testing.T) {
			got, err := sidecarMirrorConnection("edge", "lst", tt.protocol)
			require.NoError(t, err)
			assert.Equal(t, sidecarMirror{Name: "edge-lst", Type: tt.wantType, Subtype: tt.wantSubtype}, got)
		})
	}

	// The protocol names are the sidecar's own constants.
	for _, p := range []inspect.Protocol{
		inspect.Postgres, inspect.MySQL, inspect.MSSQL, inspect.MongoDB, inspect.SSH,
		inspect.HTTP, inspect.ClickHouse, inspect.GRPC, inspect.Spanner,
	} {
		_, err := sidecarMirrorConnection("edge", "lst", string(p))
		assert.NoError(t, err, "protocol %q", p)
	}

	t.Run("name is <sidecar>-<listener>", func(t *testing.T) {
		got, err := sidecarMirrorConnection("prod-sidecar", "billing-db", "postgres")
		require.NoError(t, err)
		assert.Equal(t, "prod-sidecar-billing-db", got.Name)
	})

	for _, protocol := range []string{"", "redis", "oracle", "tcp", "Postgres", "POSTGRES"} {
		t.Run("refuses "+protocol, func(t *testing.T) {
			got, err := sidecarMirrorConnection("edge", "lst", protocol)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "has no connection type")
			assert.Equal(t, sidecarMirror{}, got)
		})
	}
}

func TestSidecarSessionID(t *testing.T) {
	const sc1, sc2 = "8f0c7a52-3d43-4a4e-9a55-0b8a6f1f4c11", "0d1e2f3a-4b5c-4d6e-8f70-8192a3b4c5d6"

	id := SidecarSessionID(sc1, "s-1")
	assert.Equal(t, id, SidecarSessionID(sc1, "s-1"), "same input, same id")
	assert.Equal(t, uuid.NewSHA1(uuid.NameSpaceURL, []byte("sidecar-session:"+sc1+":s-1")).String(), id)

	parsed, err := uuid.Parse(id)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(5), parsed.Version())

	assert.NotEqual(t, id, SidecarSessionID(sc2, "s-1"), "another sidecar, another session")
	assert.NotEqual(t, id, SidecarSessionID(sc1, "s-2"), "another session id, another session")
}

func TestValidateSidecarSessionEvents(t *testing.T) {
	ev := func(seq int64, sessionID string) daemon.SessionEvent {
		return daemon.SessionEvent{Seq: seq, Event: audit.Event{
			Kind:      audit.KindStatement,
			SessionID: sidecarsession.ID(sessionID),
		}}
	}
	for _, tt := range []struct {
		name    string
		events  []daemon.SessionEvent
		wantErr string
	}{
		{name: "empty batch", events: nil},
		{name: "valid", events: []daemon.SessionEvent{ev(1, "s-1"), ev(2, "s-1"), ev(1, "s-2")}},
		{name: "session_id of 256 bytes", events: []daemon.SessionEvent{ev(1, strings.Repeat("x", 256))}},
		{name: "seq zero", events: []daemon.SessionEvent{ev(1, "s-1"), ev(0, "s-1")}, wantErr: "event 1 has seq 0"},
		{name: "negative seq", events: []daemon.SessionEvent{ev(-3, "s-1")}, wantErr: "event 0 has seq -3"},
		{name: "no session_id", events: []daemon.SessionEvent{ev(1, "s-1"), ev(1, "")}, wantErr: "event 1 has no session_id"},
		{
			name:    "session_id of 257 bytes",
			events:  []daemon.SessionEvent{ev(1, strings.Repeat("x", 257))},
			wantErr: "event 0 has a session_id longer than 256 bytes",
		},
		{
			// 86 three-byte runes: 86 characters, 258 bytes.
			name:    "session_id limit counts bytes",
			events:  []daemon.SessionEvent{ev(1, strings.Repeat("€", 86))},
			wantErr: "longer than 256 bytes",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSidecarSessionEvents(tt.events)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			var refused SidecarEventsRefused
			require.True(t, errors.As(err, &refused), "want SidecarEventsRefused, got %T: %v", err, err)
			assert.Contains(t, refused.Error(), tt.wantErr)
		})
	}
}

// A pgwire lane writes session_start before it reads the startup packet, so
// the start names nobody and the statements name the role. The first known
// principal files the session, at creation or on a row that has none.
func TestPlanSidecarSessionLearnsThePrincipal(t *testing.T) {
	anonymous := func(e *audit.Event) { e.Principal = sidecarsession.AnonymousPrincipal }
	role := func(e *audit.Event) { e.Principal = "postgres" }

	t.Run("created from an anonymous start", func(t *testing.T) {
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, nil, []daemon.SessionEvent{
			sidecarEvent(1, audit.KindSessionStart, 0, anonymous),
			sidecarEvent(2, audit.KindStatement, time.Second, role, func(e *audit.Event) { e.Statement = "SELECT 1" }),
		})
		require.NoError(t, err)
		require.NotNil(t, plan.Create)
		assert.Equal(t, "postgres", plan.Create.UserName)
		assert.Empty(t, plan.Create.UserEmail)
		assert.Nil(t, plan.User)
	})

	t.Run("nobody known yet", func(t *testing.T) {
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, nil, []daemon.SessionEvent{
			sidecarEvent(1, audit.KindSessionStart, 0, anonymous),
		})
		require.NoError(t, err)
		assert.Equal(t, sidecarsession.AnonymousPrincipal, plan.Create.UserName)
	})

	t.Run("an anonymous row learns it later", func(t *testing.T) {
		prior := existingSidecarSession(1)
		prior.Principal = sidecarsession.AnonymousPrincipal
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, prior, []daemon.SessionEvent{
			sidecarEvent(2, audit.KindStatement, time.Second, role, func(e *audit.Event) { e.Statement = "SELECT 1" }),
		})
		require.NoError(t, err)
		require.NotNil(t, plan.User)
		assert.Equal(t, sidecarUser{Name: "postgres"}, *plan.User)
	})

	t.Run("a known row keeps its principal", func(t *testing.T) {
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, existingSidecarSession(1),
			[]daemon.SessionEvent{sidecarEvent(2, audit.KindStatement, time.Second, role)})
		require.NoError(t, err)
		assert.Nil(t, plan.User)
	})

	t.Run("an anonymous row stays so while nobody is known", func(t *testing.T) {
		prior := existingSidecarSession(1)
		prior.Principal = sidecarsession.AnonymousPrincipal
		plan, err := planSidecarSession(testSidecarIdent, testSidecarSessionID, prior,
			[]daemon.SessionEvent{sidecarEvent(2, audit.KindStatement, time.Second, anonymous)})
		require.NoError(t, err)
		assert.Nil(t, plan.User)
	})
}
