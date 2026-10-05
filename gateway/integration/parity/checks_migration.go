//go:build integration && parity

package parity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/lib/pq"
)

// The migration-rehearsal checks run on the control plane booted on the
// copy seedControlPlaneCopy builds (rehearsal.go). MIG-05 migrates the run
// database down and back up, so it is declared last.

var rehearsalOnly = map[Run]Expect{MigrationRehearsal: Must}

// The checks register in one initializer, in this order. Go initializes a
// package variable after the ones it reads, and these read the fixture in
// rehearsal.go, so one register call per check would run them in dependency
// order. MIG-05 stops the control plane and rolls the schema back: it goes last.
var _ = func() bool {
	for _, ck := range []Check{checkMIG01, checkMIG02, checkMIG03, checkMIG04, checkMIG07, checkMIG06, checkMIG05} {
		register(ck)
	}
	return true
}()

// migMirrorName is the name the mirror of l must have: <sidecar>-<listener>,
// or the fallback name when that one is refused or taken. The fallback is
// what an admin sees in the connection list, so its spelling is part of the
// contract: models.SidecarMirrorFallbackName, restated.
func migMirrorName(l rehearsalListener) string {
	sc := rehearsalSidecarByName(l.Sidecar)
	preferred := sc.Name + "-" + l.Name
	if !l.Fallback {
		return preferred
	}
	sum := sha256.Sum256([]byte(sc.ID + "/" + l.Name))
	var b strings.Builder
	sep := false
	for _, r := range preferred {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if sep && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			sep = false
			continue
		}
		sep = true
	}
	return b.String() + "-" + hex.EncodeToString(sum[:])[:8]
}

// migVersion reads golang-migrate's bookkeeping row.
func migVersion(c *C) (uint, bool, error) {
	var version uint
	var dirty bool
	err := c.DB.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
	return version, dirty, err
}

var checkMIG01 = Check{
	ID:    "MIG-01",
	Title: "a control-plane database at v127 boots, migrated to the newest schema and not dirty",
	Runs:  rehearsalOnly,
	Fn: func(c *C) {
		latest, err := latestMigrationVersion()
		if err != nil {
			c.Fatalf("reading the embedded migrations: %v", err)
		}
		if latest <= rehearsalVersion+1 {
			c.Fatalf("newest embedded migration is %d; the rehearsal expects the sidecar-in-gateway migrations after %d", latest, rehearsalVersion)
		}
		version, dirty, err := migVersion(c)
		if err != nil {
			c.Fatalf("reading schema_migrations: %v", err)
		}
		if version != latest || dirty {
			c.Fatalf("schema_migrations version=%d dirty=%v, want version=%d dirty=false", version, dirty, latest)
		}
		var info struct {
			ApplicationMode string          `json:"application_mode"`
			FeatureFlags    map[string]bool `json:"feature_flags"`
		}
		c.Admin().Do(c, http.MethodGet, "/serverinfo", nil).Expect(c, http.StatusOK).JSON(c, &info)
		if info.ApplicationMode != "control-plane" {
			c.Fatalf("application_mode=%q, want control-plane", info.ApplicationMode)
		}
		if !info.FeatureFlags["beta.sidecar_listeners"] {
			c.Fatalf("beta.sidecar_listeners reads off; the copy had it on for the org")
		}
	},
}

type migConnection struct {
	ID, Name, Type, Subtype, ManagedBy, ResourceName, Listener string
	Modes                                                      [4]string
}

// migMirrors reads every connection of the seeded sidecar, keyed by listener.
func migMirrors(c *C, sidecarID string) map[string][]migConnection {
	rows, err := c.DB.Query(`
		SELECT id, name, type, COALESCE(subtype, ''), COALESCE(managed_by, ''), resource_name, sidecar_listener,
		       access_mode_connect, access_mode_exec, access_mode_runbooks, access_schema
		FROM private.connections WHERE org_id = $1 AND sidecar_id = $2`, rehearsalOrgID, sidecarID)
	if err != nil {
		c.Fatalf("reading the mirrors of sidecar %s: %v", sidecarID, err)
	}
	defer rows.Close()
	out := map[string][]migConnection{}
	for rows.Next() {
		var m migConnection
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Subtype, &m.ManagedBy, &m.ResourceName, &m.Listener,
			&m.Modes[0], &m.Modes[1], &m.Modes[2], &m.Modes[3]); err != nil {
			c.Fatalf("scanning a mirror: %v", err)
		}
		out[m.Listener] = append(out[m.Listener], m)
	}
	if err := rows.Err(); err != nil {
		c.Fatalf("reading the mirrors: %v", err)
	}
	return out
}

var checkMIG02 = Check{
	ID:    "MIG-02",
	Title: "every seeded listener has exactly one sidecar-managed mirror; a taken or invalid name gets the fallback name",
	Runs:  rehearsalOnly,
	Fn: func(c *C) {
		bySidecar := map[string]map[string][]migConnection{}
		for _, sc := range rehearsalSidecars {
			bySidecar[sc.Name] = migMirrors(c, sc.ID)
		}
		seen := map[string]int{}
		for _, l := range rehearsalListeners {
			seen[l.Sidecar]++
			got := bySidecar[l.Sidecar][l.Name]
			if len(got) != 1 {
				c.Fatalf("listener %s/%q has %d mirrors, want 1: %+v", l.Sidecar, l.Name, len(got), got)
			}
			m, want := got[0], migMirrorName(l)
			if m.Name != want {
				c.Fatalf("listener %s/%q is mirrored as %q, want %q", l.Sidecar, l.Name, m.Name, want)
			}
			if m.ManagedBy != "sidecar" || m.Type != l.Type || m.Subtype != l.Subtype || m.ResourceName != m.Name {
				c.Fatalf("mirror %q: managed_by=%q type=%s/%s resource=%q, want sidecar %s/%s resource=%q",
					m.Name, m.ManagedBy, m.Type, m.Subtype, m.ResourceName, l.Type, l.Subtype, m.Name)
			}
			if m.Modes != [4]string{"disabled", "disabled", "disabled", "disabled"} {
				c.Fatalf("mirror %q: access modes connect/exec/runbooks/schema=%v, want all disabled", m.Name, m.Modes)
			}
			var api struct {
				Name      string  `json:"name"`
				Type      string  `json:"type"`
				SubType   string  `json:"subtype"`
				ManagedBy *string `json:"managed_by"`
			}
			c.Admin().Do(c, http.MethodGet, "/connections/"+url.PathEscape(m.Name), nil).Expect(c, http.StatusOK).JSON(c, &api)
			if api.Name != m.Name || api.Type != l.Type || api.SubType != l.Subtype || api.ManagedBy == nil || *api.ManagedBy != "sidecar" {
				c.Fatalf("GET /connections/%s answered %+v, want a %s/%s connection managed by sidecar", m.Name, api, l.Type, l.Subtype)
			}
		}
		for name, mirrors := range bySidecar {
			if len(mirrors) != seen[name] {
				c.Fatalf("sidecar %s has mirrors for %d listeners, want %d: %+v", name, len(mirrors), seen[name], mirrors)
			}
		}
		// The ordinary connection that held the preferred name is left alone.
		var managedBy, sidecarID, resource, typ string
		err := c.DB.QueryRow(`SELECT COALESCE(managed_by, ''), COALESCE(sidecar_id::text, ''), resource_name, type
			FROM private.connections WHERE org_id = $1 AND name = $2`, rehearsalOrgID, rehearsalCollision).
			Scan(&managedBy, &sidecarID, &resource, &typ)
		if err != nil {
			c.Fatalf("reading the ordinary connection %q: %v", rehearsalCollision, err)
		}
		if managedBy != "" || sidecarID != "" || resource != rehearsalCollision || typ != "database" {
			c.Fatalf("ordinary connection %q changed: managed_by=%q sidecar_id=%q resource=%q type=%q",
				rehearsalCollision, managedBy, sidecarID, resource, typ)
		}
	},
}

// migServedListener is the part of a served listener the bound rules reach.
type migServedListener struct {
	Name       string `json:"name"`
	Guardrails *struct {
		Rules []struct {
			Name string `json:"name"`
		} `json:"rules"`
	} `json:"guardrails"`
	Mask *struct {
		Rules []struct {
			Name string `json:"name"`
		} `json:"rules"`
	} `json:"mask"`
	Analyzer *struct {
		ApprovalRule string `json:"approval_rule"`
		High         string `json:"high"`
		Medium       string `json:"medium"`
		MaxCalls     int    `json:"max_calls"`
		FailOpen     *bool  `json:"fail_open"`
	} `json:"analyzer"`
}

// migSpecNames is the rule names a seeded spec contributes, in order.
func migSpecNames(c *C, ruleName string) []string {
	for _, r := range rehearsalRules {
		if r.Name != ruleName {
			continue
		}
		var spec struct {
			Rules []struct {
				Name string `json:"name"`
			} `json:"rules"`
		}
		if err := json.Unmarshal([]byte(r.Spec), &spec); err != nil {
			c.Fatalf("seeded spec of %s: %v", ruleName, err)
		}
		var out []string
		for _, e := range spec.Rules {
			out = append(out, e.Name)
		}
		return out
	}
	c.Fatalf("no seeded rule %s", ruleName)
	return nil
}

// migExpectedRules is, per listener of one sidecar, the rule entries a kind
// must serve, in position order.
func migExpectedRules(c *C, sidecar, kind string) map[string][]string {
	bound := []rehearsalBinding{}
	for _, b := range rehearsalBindings {
		if b.Sidecar == sidecar && b.Kind == kind {
			bound = append(bound, b)
		}
	}
	slices.SortStableFunc(bound, func(a, b rehearsalBinding) int { return a.Position - b.Position })
	out := map[string][]string{}
	for _, b := range bound {
		out[b.Listener] = append(out[b.Listener], migSpecNames(c, b.Rule)...)
	}
	return out
}

var checkMIG03 = Check{
	ID:    "MIG-03",
	Title: "rules bound before the migration are still served, in position order, in each sidecar's handshake",
	Runs:  rehearsalOnly,
	Fn: func(c *C) {
		for _, name := range []string{"payments", "edge", "crm"} {
			sc := rehearsalSidecarByName(name)
			var served struct {
				License   string              `json:"license"`
				Listeners []migServedListener `json:"listeners"`
			}
			c.Sidecar(sc.Token).Do(c, http.MethodPost, "/sidecars/handshake",
				map[string]string{"version": rehearsalHandshakeVersion}).Expect(c, http.StatusOK).JSON(c, &served)
			if served.License == "" {
				c.Fatalf("sidecar %s: the handshake served no license", name)
			}
			guardrails := migExpectedRules(c, name, "guardrail")
			masking := migExpectedRules(c, name, "datamasking")
			analyzers := migExpectedRules(c, name, "analyzer")
			listed := 0
			for _, l := range rehearsalListeners {
				if l.Sidecar != name {
					continue
				}
				listed++
				var got *migServedListener
				for i := range served.Listeners {
					if served.Listeners[i].Name == l.Name {
						got = &served.Listeners[i]
					}
				}
				if got == nil {
					c.Fatalf("sidecar %s: listener %q is missing from the handshake", name, l.Name)
				}
				var gotGuardrails, gotMask []string
				if got.Guardrails != nil {
					for _, r := range got.Guardrails.Rules {
						gotGuardrails = append(gotGuardrails, r.Name)
					}
				}
				if got.Mask != nil {
					for _, r := range got.Mask.Rules {
						gotMask = append(gotMask, r.Name)
					}
				}
				if !slices.Equal(gotGuardrails, guardrails[l.Name]) {
					c.Fatalf("sidecar %s listener %q serves guardrails %v, want %v", name, l.Name, gotGuardrails, guardrails[l.Name])
				}
				if !slices.Equal(gotMask, masking[l.Name]) {
					c.Fatalf("sidecar %s listener %q serves mask rules %v, want %v", name, l.Name, gotMask, masking[l.Name])
				}
				if _, bound := analyzers[l.Name]; bound {
					a := got.Analyzer
					// The rule owns the verdicts; the listener keeps its own
					// budget and fail_open.
					if a == nil || a.ApprovalRule != rehearsalApprovalRule || a.High != "require_review" || a.Medium != "warn" ||
						a.MaxCalls != 40 || a.FailOpen == nil || *a.FailOpen {
						c.Fatalf("sidecar %s listener %q serves analyzer %+v, want the bound rule over the listener's max_calls 40 and fail_open false",
							name, l.Name, a)
					}
				}
			}
			if len(served.Listeners) != listed {
				c.Fatalf("sidecar %s: the handshake serves %d listeners, want %d", name, len(served.Listeners), listed)
			}
		}
		// A sidecar on its own file keeps the instruction, not a document.
		var disk map[string]any
		c.Sidecar(rehearsalSidecarByName("legacy").Token).Do(c, http.MethodGet, "/sidecars/configuration", nil).
			Expect(c, http.StatusOK).JSON(c, &disk)
		if disk["load_from_disk"] != true || disk["license"] == "" || len(disk) != 2 {
			c.Fatalf("file-mode sidecar was served %v, want only load_from_disk true and the license", disk)
		}
	},
}

type migSidecarResponse struct {
	ID              string          `json:"id"`
	OrgID           string          `json:"org_id"`
	Name            string          `json:"name"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
	Configuration   json.RawMessage `json:"configuration"`
	Version         string          `json:"version"`
	ServedRevision  string          `json:"served_revision"`
	AppliedRevision string          `json:"applied_revision"`
	LastOutcome     string          `json:"last_outcome"`
	LastError       string          `json:"last_error"`
	LastSeenAt      *time.Time      `json:"last_seen_at"`
	BoundRules      []struct {
		Kind         string `json:"kind"`
		RuleName     string `json:"rule_name"`
		ListenerName string `json:"listener_name"`
	} `json:"bound_rules"`
	BoundRulesUnavailable bool `json:"bound_rules_unavailable"`
}

// migSameJSON compares two documents by value, not by spelling.
func migSameJSON(a, b []byte) (bool, error) {
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false, err
	}
	return reflect.DeepEqual(x, y), nil
}

var checkMIG04 = Check{
	ID:    "MIG-04",
	Title: "seeded sidecar admin data reads back unchanged: sidecars, bound rules, slack channels, approval rule",
	Runs:  rehearsalOnly,
	Fn: func(c *C) {
		var list []migSidecarResponse
		c.Admin().Do(c, http.MethodGet, "/sidecars", nil).Expect(c, http.StatusOK).JSON(c, &list)
		byName := map[string]migSidecarResponse{}
		for _, s := range list {
			byName[s.Name] = s
		}
		for _, sc := range rehearsalSidecars {
			got, ok := byName[sc.Name]
			if !ok {
				c.Fatalf("GET /sidecars lacks %s", sc.Name)
			}
			if got.ID != sc.ID || got.OrgID != rehearsalOrgID || got.CreatedBy != sc.CreatedBy || !got.CreatedAt.Equal(sc.CreatedAt) {
				c.Fatalf("sidecar %s reads id=%s org=%s created_by=%s created_at=%s, want %s %s %s %s",
					sc.Name, got.ID, got.OrgID, got.CreatedBy, got.CreatedAt, sc.ID, rehearsalOrgID, sc.CreatedBy, sc.CreatedAt)
			}
			same, err := migSameJSON(got.Configuration, []byte(sc.Config))
			if err != nil || !same {
				c.Fatalf("sidecar %s configuration reads %s, want the seeded %s (err=%v)", sc.Name, got.Configuration, sc.Config, err)
			}
			if got.BoundRulesUnavailable {
				c.Fatalf("sidecar %s: bound_rules_unavailable", sc.Name)
			}
			var gotBound, wantBound []string
			for _, b := range got.BoundRules {
				gotBound = append(gotBound, b.Kind+"/"+b.RuleName+"@"+b.ListenerName)
			}
			for _, b := range rehearsalBindings {
				if b.Sidecar == sc.Name {
					wantBound = append(wantBound, b.Kind+"/"+b.Rule+"@"+b.Listener)
				}
			}
			slices.Sort(gotBound)
			slices.Sort(wantBound)
			if !slices.Equal(gotBound, wantBound) {
				c.Fatalf("sidecar %s bound_rules=%v, want %v", sc.Name, gotBound, wantBound)
			}
			var detail migSidecarResponse
			c.Admin().Do(c, http.MethodGet, "/sidecars/"+sc.ID, nil).Expect(c, http.StatusOK).JSON(c, &detail)
			if detail.Name != sc.Name || len(detail.BoundRules) != len(wantBound) {
				c.Fatalf("GET /sidecars/%s answered name=%s with %d bound rules, want %s with %d", sc.ID, detail.Name, len(detail.BoundRules), sc.Name, len(wantBound))
			}
		}
		// No check handshakes legacy, so what its last handshake before the
		// copy recorded must read back as it was.
		legacy, seeded := byName["legacy"], rehearsalSidecarByName("legacy")
		if legacy.Version != seeded.ReportedVersion || legacy.LastOutcome != seeded.LastOutcome ||
			legacy.ServedRevision != "" || legacy.AppliedRevision != "" || legacy.LastError != "" ||
			legacy.LastSeenAt == nil || !legacy.LastSeenAt.Equal(seeded.LastSeenAt) {
			c.Fatalf("legacy runtime state reads version=%q outcome=%q served=%q applied=%q error=%q seen=%v, want %q %q and %s",
				legacy.Version, legacy.LastOutcome, legacy.ServedRevision, legacy.AppliedRevision, legacy.LastError,
				legacy.LastSeenAt, seeded.ReportedVersion, seeded.LastOutcome, seeded.LastSeenAt)
		}

		for _, name := range []string{"payments", "crm", "edge"} {
			var channels struct {
				Listeners []struct {
					Name     string   `json:"name"`
					Channels []string `json:"channels"`
				} `json:"listeners"`
			}
			c.Admin().Do(c, http.MethodGet, "/sidecars/"+name+"/slack-channels", nil).Expect(c, http.StatusOK).JSON(c, &channels)
			got := map[string][]string{}
			for _, l := range channels.Listeners {
				got[l.Name] = l.Channels
			}
			want := map[string][]string{}
			for _, s := range rehearsalSlack {
				if s.Sidecar == name {
					want[s.Listener] = s.Channels
				}
			}
			if !reflect.DeepEqual(got, want) {
				c.Fatalf("sidecar %s slack channels read %v, want %v", name, got, want)
			}
		}

		var rule struct {
			Name            string   `json:"name"`
			AccessType      string   `json:"access_type"`
			ReviewersGroups []string `json:"reviewers_groups"`
			MinApprovals    *int     `json:"min_approvals"`
		}
		c.Admin().Do(c, http.MethodGet, "/access-requests/rules/"+rehearsalApprovalRule, nil).Expect(c, http.StatusOK).JSON(c, &rule)
		if rule.AccessType != "sidecar" || !slices.Equal(rule.ReviewersGroups, []string{"admin", "dba"}) ||
			rule.MinApprovals == nil || *rule.MinApprovals != 1 {
			c.Fatalf("approval rule reads %+v, want a sidecar rule for admin,dba with min_approvals 1", rule)
		}
	},
}

var checkMIG07 = Check{
	ID:    "MIG-07",
	Title: "sidecar reviews filed before the migration still read back to their sidecar, status and rule intact",
	Runs:  rehearsalOnly,
	Fn: func(c *C) {
		payments := c.Sidecar(rehearsalSidecarByName("payments").Token)
		for _, r := range rehearsalReviews {
			var got struct {
				ID              string     `json:"id"`
				Status          string     `json:"status"`
				ListenerName    string     `json:"listener_name"`
				ApprovalRule    string     `json:"approval_rule"`
				CreatedAt       time.Time  `json:"created_at"`
				DecidedAt       *time.Time `json:"decided_at"`
				RejectionReason *string    `json:"rejection_reason"`
			}
			payments.Do(c, http.MethodGet, "/sidecars/reviews/"+r.ID, nil).Expect(c, http.StatusOK).JSON(c, &got)
			if got.ID != r.ID || got.Status != r.Status || got.ListenerName != r.Listener || got.ApprovalRule != r.Rule ||
				!got.CreatedAt.Equal(r.CreatedAt) {
				c.Fatalf("review %s reads %+v, want status=%s listener=%s rule=%s created_at=%s",
					r.ID, got, r.Status, r.Listener, r.Rule, r.CreatedAt)
			}
			if decided := r.Status != "PENDING"; decided != (got.DecidedAt != nil) {
				c.Fatalf("review %s (%s) decided_at=%v", r.ID, r.Status, got.DecidedAt)
			}
			if r.RejectionReason != "" && (got.RejectionReason == nil || *got.RejectionReason != r.RejectionReason) {
				c.Fatalf("review %s rejection_reason=%v, want %q", r.ID, got.RejectionReason, r.RejectionReason)
			}
		}
		var pending []struct {
			ID string `json:"id"`
		}
		payments.Do(c, http.MethodGet, "/sidecars/reviews?status=PENDING", nil).Expect(c, http.StatusOK).JSON(c, &pending)
		if len(pending) != 1 || pending[0].ID != rehearsalReviews[0].ID {
			c.Fatalf("pending reviews of payments read %+v, want only %s", pending, rehearsalReviews[0].ID)
		}
		// Another sidecar's token still cannot read them.
		c.Sidecar(rehearsalSidecarByName("edge").Token).
			Do(c, http.MethodGet, "/sidecars/reviews/"+rehearsalReviews[0].ID, nil).Expect(c, http.StatusNotFound)
	},
}

var checkMIG06 = Check{
	ID:    "MIG-06",
	Title: "rules bound to listeners are bound through the mirror connections, in the same position",
	Runs:  map[Run]Expect{MigrationRehearsal: PendingOn("ENG-525")},
	Fn: func(c *C) {
		mirrorID := map[string]string{}
		mirrorName := map[string]string{}
		for _, sc := range rehearsalSidecars {
			for listener, ms := range migMirrors(c, sc.ID) {
				if len(ms) == 1 {
					mirrorID[sc.Name+"/"+listener] = ms[0].ID
					mirrorName[sc.Name+"/"+listener] = ms[0].Name
				}
			}
		}
		for _, b := range rehearsalBindings {
			key := b.Sidecar + "/" + b.Listener
			if mirrorID[key] == "" {
				c.Fatalf("listener %s has no mirror to bind %s rule %q through", key, b.Kind, b.Rule)
			}
			switch b.Kind {
			case "guardrail", "datamasking":
				rules, junction := "private.guardrail_rules", "private.guardrail_rules_connections"
				if b.Kind == "datamasking" {
					rules, junction = "private.datamasking_rules", "private.datamasking_rules_connections"
				}
				var position int
				err := c.DB.QueryRow(`SELECT j.position FROM `+junction+` j JOIN `+rules+` r ON r.id = j.rule_id
					WHERE r.org_id = $1 AND r.name = $2 AND j.connection_id = $3`, rehearsalOrgID, b.Rule, mirrorID[key]).Scan(&position)
				if err != nil {
					c.Fatalf("%s rule %q is not bound to mirror %q of %s: %v", b.Kind, b.Rule, mirrorName[key], key, err)
				}
				if position != b.Position {
					c.Fatalf("%s rule %q on mirror %q has position %d, want %d", b.Kind, b.Rule, mirrorName[key], position, b.Position)
				}
			case "analyzer":
				var names pq.StringArray
				err := c.DB.QueryRow(`SELECT connection_names FROM private.ai_session_analyzer_rules WHERE org_id = $1 AND name = $2`,
					rehearsalOrgID, b.Rule).Scan(&names)
				if err != nil {
					c.Fatalf("reading analyzer rule %q: %v", b.Rule, err)
				}
				if !slices.Contains(names, mirrorName[key]) {
					c.Fatalf("analyzer rule %q targets connections %v, want mirror %q of %s", b.Rule, names, mirrorName[key], key)
				}
			}
		}
	},
}

// migAfterDown asserts the database migrateRehearsal left at rehearsalVersion:
// the mirrors are gone and what the copy held before the boot is intact.
func migAfterDown(c *C) error {
	version, dirty, err := migVersion(c)
	if err != nil {
		return fmt.Errorf("reading schema_migrations: %w", err)
	}
	if version != rehearsalVersion || dirty {
		return fmt.Errorf("after the down migration schema_migrations version=%d dirty=%v, want %d clean", version, dirty, rehearsalVersion)
	}
	var columns int
	err = c.DB.QueryRow(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'private' AND table_name = 'connections' AND column_name IN ('sidecar_id', 'sidecar_listener')`).Scan(&columns)
	if err != nil || columns != 0 {
		return fmt.Errorf("connections keeps %d mirror columns after the down migration (err=%v)", columns, err)
	}
	var names []string
	for _, l := range rehearsalListeners {
		names = append(names, migMirrorName(l))
	}
	var left int
	err = c.DB.QueryRow(`SELECT (SELECT count(*) FROM private.connections WHERE org_id = $1 AND (name = ANY($2) OR managed_by = 'sidecar'))
		+ (SELECT count(*) FROM private.resources WHERE org_id = $1 AND name = ANY($2))`, rehearsalOrgID, pq.StringArray(names)).Scan(&left)
	if err != nil || left != 0 {
		return fmt.Errorf("%d mirror connections or resources survive the down migration (err=%v)", left, err)
	}
	var ordinary int
	err = c.DB.QueryRow(`SELECT count(*) FROM private.connections c JOIN private.resources r ON r.org_id = c.org_id AND r.name = c.resource_name
		WHERE c.org_id = $1 AND c.name = $2`, rehearsalOrgID, rehearsalCollision).Scan(&ordinary)
	if err != nil || ordinary != 1 {
		return fmt.Errorf("the ordinary connection %q and its resource did not survive the down migration (rows=%d err=%v)", rehearsalCollision, ordinary, err)
	}
	for _, sc := range rehearsalSidecars {
		var same bool
		err := c.DB.QueryRow(`SELECT configuration = $2::jsonb FROM private.sidecars WHERE id = $1`, sc.ID, sc.Config).Scan(&same)
		if err != nil || !same {
			return fmt.Errorf("sidecar %s: configuration changed or row gone after the down migration (err=%v)", sc.Name, err)
		}
	}
	for _, r := range rehearsalRules {
		table := map[string]string{"guardrail": "guardrail_rules", "datamasking": "datamasking_rules", "analyzer": "ai_session_analyzer_rules"}[r.Kind]
		var same bool
		err := c.DB.QueryRow(`SELECT sidecar_spec = $3::jsonb FROM private.`+table+` WHERE org_id = $1 AND name = $2`,
			rehearsalOrgID, r.Name, r.Spec).Scan(&same)
		if err != nil || !same {
			return fmt.Errorf("%s rule %q: sidecar_spec changed or row gone after the down migration (err=%v)", r.Kind, r.Name, err)
		}
	}
	want := map[string]map[string]int{}
	for _, b := range rehearsalBindings {
		if want[b.Kind] == nil {
			want[b.Kind] = map[string]int{}
		}
		want[b.Kind][b.Rule+"@"+rehearsalSidecarByName(b.Sidecar).ID+"/"+b.Listener] = b.Position
	}
	for kind, rows := range want {
		table, column, err := rulesListenersTable(kind)
		if err != nil {
			return err
		}
		got, err := migListenerBindings(c, table, column)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, rows) {
			return fmt.Errorf("%s after the down migration holds %v, want %v", table, got, rows)
		}
	}
	var slack, reviews int
	err = c.DB.QueryRow(`SELECT (SELECT count(*) FROM private.sidecar_slack_channels WHERE org_id = $1),
		(SELECT count(*) FROM private.reviews WHERE org_id = $1 AND sidecar_id IS NOT NULL)`, rehearsalOrgID).Scan(&slack, &reviews)
	if err != nil || slack != len(rehearsalSlack) || reviews != len(rehearsalReviews) {
		return fmt.Errorf("after the down migration: %d slack channel rows and %d sidecar reviews, want %d and %d (err=%v)",
			slack, reviews, len(rehearsalSlack), len(rehearsalReviews), err)
	}
	return nil
}

// migListenerBindings reads one *_rules_listeners junction for the seeded
// sidecars as rule@sidecar/listener -> position.
func migListenerBindings(c *C, table, column string) (map[string]int, error) {
	var ids []string
	for _, sc := range rehearsalSidecars {
		ids = append(ids, sc.ID)
	}
	rows, err := c.DB.Query(`SELECT `+column+`, sidecar_id, listener_name, position FROM private.`+table+`
		WHERE org_id = $1 AND sidecar_id::text = ANY($2)`, rehearsalOrgID, pq.StringArray(ids))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", table, err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var rule, sidecar, listener string
		var position int
		if err := rows.Scan(&rule, &sidecar, &listener, &position); err != nil {
			return nil, err
		}
		out[rule+"@"+sidecar+"/"+listener] = position
	}
	return out, rows.Err()
}

var checkMIG05 = Check{
	ID:    "MIG-05",
	Title: "rolling the copy back to v127 removes the mirrors, keeps every seeded binding, and rolls forward again",
	Runs:  rehearsalOnly,
	Fn: func(c *C) {
		// Last check of the run. An operator stops the control plane before
		// rolling its schema back, so the process goes down first.
		c.Shutdown()
		latest, err := latestMigrationVersion()
		if err != nil {
			c.Fatalf("reading the embedded migrations: %v", err)
		}
		downErr := migrateRehearsal(c.DBURI, func(m *migrate.Migrate) error { return m.Migrate(rehearsalVersion) })
		var checkErr error
		if downErr == nil {
			checkErr = migAfterDown(c)
		}
		upErr := migrateRehearsal(c.DBURI, func(m *migrate.Migrate) error {
			if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				return err
			}
			return nil
		})
		switch {
		case downErr != nil:
			c.Fatalf("migrating down to %d: %v (up again: %v)", rehearsalVersion, downErr, upErr)
		case checkErr != nil:
			c.Fatalf("%v (up again: %v)", checkErr, upErr)
		case upErr != nil:
			c.Fatalf("migrating up again from %d: %v", rehearsalVersion, upErr)
		}
		version, dirty, err := migVersion(c)
		if err != nil || version != latest || dirty {
			c.Fatalf("after rolling forward again schema_migrations version=%d dirty=%v err=%v, want %d clean", version, dirty, err, latest)
		}
		// The backfill mirrors again every listener it can name; the fallback
		// names wait for the startup reconcile, as on a first upgrade.
		for _, l := range rehearsalListeners {
			if l.Fallback {
				continue
			}
			var n int
			err := c.DB.QueryRow(`SELECT count(*) FROM private.connections WHERE org_id = $1 AND name = $2 AND managed_by = 'sidecar'
				AND sidecar_id = $3 AND sidecar_listener = $4`, rehearsalOrgID, migMirrorName(l), rehearsalSidecarByName(l.Sidecar).ID, l.Name).Scan(&n)
			if err != nil || n != 1 {
				c.Fatalf("after rolling forward again listener %s/%q has %d mirrors named %q (err=%v), want 1", l.Sidecar, l.Name, n, migMirrorName(l), err)
			}
		}
	},
}
