//go:build integration && parity

package parity

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// Sidecar reviews (RV) and sidecar rules (RUL), as a control-plane customer
// drives them today: an admin authors the rules, a sidecar files and claims
// reviews with its own token.
//
// On the gateway with beta.sidecar_listeners on, reviews wait for ENG-526 and
// rule bindings for ENG-525. Nothing here runs on gateway-flag-off except the
// token refusal: today's gateway has no sidecar reviews or bound rules to keep,
// and ENG-524 may legitimately change what it answers for them.
var (
	rvReviewRuns = map[Run]Expect{ControlPlane: Must, GatewayFlagOn: PendingOn("ENG-526")}
	rvRuleRuns   = map[Run]Expect{ControlPlane: Must, GatewayFlagOn: PendingOn("ENG-525")}
)

const (
	rvLane         = "appdb"
	rvOtherLane    = "reporting"
	rvVersion      = "1.191.0"
	rvRevisionHdr  = "hoop-sidecar-config-revision"
	rvAdminGroup   = "admin"
	rvSidecarModel = "m"
)

// rvConfig is a sidecar document with the analyzer section a lane holding
// statements needs, and the given lanes.
func rvConfig(lanes ...map[string]any) map[string]any {
	return map[string]any{
		"analyzer":  map[string]any{"provider": "anthropic", "model": rvSidecarModel},
		"listeners": lanes,
	}
}

// rvListener is one postgres lane. analyzer is its own analyzer block, or nil.
func rvListener(name string, port int, analyzer map[string]any) map[string]any {
	l := map[string]any{
		"name": name, "protocol": "postgres",
		"listen": fmt.Sprintf("127.0.0.1:%d", port), "upstream": "127.0.0.1:5432",
	}
	if analyzer != nil {
		l["analyzer"] = analyzer
	}
	return l
}

// rvHold is a lane analyzer block that holds high-risk statements for rule.
func rvHold(rule string) map[string]any {
	return map[string]any{"high": "require_review", "approval_rule": rule}
}

type rvSidecar struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

func rvCreateSidecar(c *C, suffix string, cfg map[string]any) rvSidecar {
	var sc rvSidecar
	c.Admin().Do(c, http.MethodPost, "/sidecars", map[string]any{"name": c.Name(suffix), "configuration": cfg}).
		Expect(c, http.StatusCreated).JSON(c, &sc)
	if sc.ID == "" || sc.Token == "" {
		c.Fatalf("sidecar %s created with id=%q and an empty-or-not token (%t)", c.Name(suffix), sc.ID, sc.Token != "")
	}
	return sc
}

type rvAccessRule struct {
	Name                 string   `json:"name"`
	AccessType           string   `json:"access_type"`
	ConnectionNames      []string `json:"connection_names"`
	ReviewersGroups      []string `json:"reviewers_groups"`
	ForceApprovalGroups  []string `json:"force_approval_groups"`
	AllGroupsMustApprove bool     `json:"all_groups_must_approve"`
	MinApprovals         *int     `json:"min_approvals"`
	ManagedBy            *string  `json:"managed_by"`
}

// rvCreateApprovalRule stores a sidecar access request rule. body overrides
// the defaults: one approval from the admin group.
func rvCreateApprovalRule(c *C, suffix string, body map[string]any) string {
	name := c.Name(suffix)
	req := map[string]any{
		"name": name, "access_type": "sidecar",
		"reviewers_groups": []string{rvAdminGroup}, "approval_required_groups": []string{},
		"force_approval_groups": []string{}, "min_approvals": 1,
	}
	for k, v := range body {
		req[k] = v
	}
	var got rvAccessRule
	c.Admin().Do(c, http.MethodPost, "/access-requests/rules", req).Expect(c, http.StatusCreated).JSON(c, &got)
	if got.Name != name || got.AccessType != "sidecar" {
		c.Fatalf("access request rule created as name=%q access_type=%q, want %q sidecar", got.Name, got.AccessType, name)
	}
	return name
}

// rvHandshake is the sidecar's handshake. The served document is decoded the
// way a sidecar decodes it, strictly: a key the sidecar does not declare
// refuses the whole document.
func rvHandshake(c *C, token string) (daemon.Config, string) {
	r := c.Sidecar(token).Do(c, http.MethodPost, "/sidecars/handshake", map[string]string{"version": rvVersion}).
		Expect(c, http.StatusOK)
	var cfg daemon.Config
	dec := json.NewDecoder(bytes.NewReader(r.Body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		c.Fatalf("a sidecar would refuse the served document: %v\n%s", err, truncate(r.Body))
	}
	rev := r.Header.Get(rvRevisionHdr)
	if rev == "" {
		c.Fatalf("handshake answered 200 with no %s header", rvRevisionHdr)
	}
	return cfg, rev
}

func rvServedLane(c *C, cfg daemon.Config, name string) daemon.ListenerConfig {
	for _, l := range cfg.Listeners {
		if l.Name == name {
			return l
		}
	}
	c.Fatalf("served document has no listener %q", name)
	return daemon.ListenerConfig{}
}

func rvLaneHasGuardrail(l daemon.ListenerConfig, rule string) bool {
	if l.Guardrails == nil {
		return false
	}
	for _, r := range l.Guardrails.Rules {
		if r.Name == rule {
			return true
		}
	}
	return false
}

// rvReviewing is one sidecar whose lane holds statements under its own
// approval rule.
type rvReviewing struct {
	sc   rvSidecar
	rule string
	lane string
}

func rvNewReviewing(c *C) rvReviewing {
	rule := rvCreateApprovalRule(c, "approvers", nil)
	sc := rvCreateSidecar(c, "sc", rvConfig(
		rvListener(rvLane, 15432, rvHold(rule)),
		rvListener(rvOtherLane, 15433, nil),
	))
	return rvReviewing{sc: sc, rule: rule, lane: rvLane}
}

type rvReviewResp struct {
	Forward bool `json:"forward"`
	Review  struct {
		ID                    string  `json:"id"`
		Session               string  `json:"session"`
		Status                string  `json:"status"`
		AccessRequestRuleName *string `json:"access_request_rule_name"`
		MinApprovals          *int    `json:"min_approvals"`
		SidecarID             *string `json:"sidecar_id"`
		ListenerName          *string `json:"listener_name"`
		ReviewGroupsData      []struct {
			Group  string `json:"group"`
			Status string `json:"status"`
		} `json:"review_groups_data"`
	} `json:"review"`
}

type rvStatus struct {
	ID              string  `json:"id"`
	Status          string  `json:"status"`
	ListenerName    string  `json:"listener_name"`
	ApprovalRule    string  `json:"approval_rule"`
	DecidedAt       *string `json:"decided_at"`
	RejectionReason *string `json:"rejection_reason"`
}

func rvReviewBody(lane, rule, stmt string) map[string]string {
	return map[string]string{
		"listener_name": lane, "approval_rule": rule,
		"payload": base64.StdEncoding.EncodeToString([]byte(stmt)),
	}
}

// send files or resends stmt and returns the status code and answer.
func (f rvReviewing) send(c *C, stmt string) (int, rvReviewResp) {
	return rvSendReview(c, f.sc.Token, f.lane, f.rule, stmt)
}

func rvSendReview(c *C, token, lane, rule, stmt string) (int, rvReviewResp) {
	r := c.Sidecar(token).Do(c, http.MethodPost, "/sidecars/reviews", rvReviewBody(lane, rule, stmt)).
		Expect(c, http.StatusOK, http.StatusCreated)
	var out rvReviewResp
	r.JSON(c, &out)
	if out.Review.ID == "" {
		c.Fatalf("review answer carries no review id: %s", truncate(r.Body))
	}
	return r.Status, out
}

// file sends a statement no review exists for yet.
func (f rvReviewing) file(c *C, stmt string) rvReviewResp {
	code, out := f.send(c, stmt)
	if code != http.StatusCreated || out.Forward || out.Review.Status != "PENDING" {
		c.Fatalf("filing %q: status=%d forward=%v review=%s, want 201 forward=false PENDING",
			stmt, code, out.Forward, out.Review.Status)
	}
	return out
}

func (f rvReviewing) claim(c *C, id string) rvReviewResp {
	var out rvReviewResp
	c.Sidecar(f.sc.Token).Do(c, http.MethodPost, "/sidecars/reviews/"+id+"/claim", nil).
		Expect(c, http.StatusOK).JSON(c, &out)
	return out
}

func (f rvReviewing) status(c *C, id string) rvStatus {
	var out rvStatus
	c.Sidecar(f.sc.Token).Do(c, http.MethodGet, "/sidecars/reviews/"+id, nil).Expect(c, http.StatusOK).JSON(c, &out)
	return out
}

// rvDecide is a human acting on the review as admin.
func rvDecide(c *C, id string, body map[string]any) Resp {
	return c.Admin().Do(c, http.MethodPut, "/reviews/"+id, body)
}

func rvApprove(c *C, id string) {
	var rev struct {
		Status string `json:"status"`
	}
	rvDecide(c, id, map[string]any{"status": "APPROVED"}).Expect(c, http.StatusOK).JSON(c, &rev)
	if rev.Status != "APPROVED" {
		c.Fatalf("approving review %s left it %s", id, rev.Status)
	}
}

func rvWantAnswer(c *C, what string, got rvReviewResp, forward bool, status, id string) {
	if got.Forward != forward || got.Review.Status != status || (id != "" && got.Review.ID != id) {
		c.Fatalf("%s: forward=%v status=%s id=%s, want forward=%v status=%s id=%s",
			what, got.Forward, got.Review.Status, got.Review.ID, forward, status, id)
	}
}

var _ = register(Check{
	ID:    "RV-01",
	Title: "the approval rule a listener holds statements under is served to the sidecar on handshake",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		f := rvNewReviewing(c)
		var rule rvAccessRule
		c.Admin().Do(c, http.MethodGet, "/access-requests/rules/"+f.rule, nil).Expect(c, http.StatusOK).JSON(c, &rule)
		if rule.AccessType != "sidecar" || len(rule.ConnectionNames) != 0 ||
			len(rule.ReviewersGroups) != 1 || rule.ReviewersGroups[0] != rvAdminGroup {
			c.Fatalf("stored approval rule = %+v, want a sidecar rule with no connections reviewed by %q", rule, rvAdminGroup)
		}
		cfg, _ := rvHandshake(c, f.sc.Token)
		lane := rvServedLane(c, cfg, rvLane)
		if lane.Analyzer == nil || lane.Analyzer.ApprovalRule != f.rule || lane.Analyzer.HighRisk != "require_review" {
			c.Fatalf("served %s analyzer = %+v, want high=require_review approval_rule=%q", rvLane, lane.Analyzer, f.rule)
		}
		if cfg.Analyzer == nil || cfg.Analyzer.Provider != "anthropic" {
			c.Fatalf("served top-level analyzer = %+v, want the stored provider", cfg.Analyzer)
		}
		if other := rvServedLane(c, cfg, rvOtherLane); other.Analyzer != nil {
			c.Fatalf("served %s carries an analyzer block nobody bound: %+v", rvOtherLane, other.Analyzer)
		}
	},
})

var _ = register(Check{
	ID:    "RV-02",
	Title: "a held statement files a PENDING review that names the sidecar, the listener and the rule's policy",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		f := rvNewReviewing(c)
		got := f.file(c, "DELETE FROM payments WHERE id = 1")
		rv := got.Review
		switch {
		case rv.Session == "":
			c.Fatalf("filed review has no session")
		case rv.SidecarID == nil || *rv.SidecarID != f.sc.ID:
			c.Fatalf("filed review sidecar_id=%v, want %s", rv.SidecarID, f.sc.ID)
		case rv.ListenerName == nil || *rv.ListenerName != rvLane:
			c.Fatalf("filed review listener_name=%v, want %s", rv.ListenerName, rvLane)
		case rv.AccessRequestRuleName == nil || *rv.AccessRequestRuleName != f.rule:
			c.Fatalf("filed review access_request_rule_name=%v, want %s", rv.AccessRequestRuleName, f.rule)
		case rv.MinApprovals == nil || *rv.MinApprovals != 1:
			c.Fatalf("filed review min_approvals=%v, want 1", rv.MinApprovals)
		case len(rv.ReviewGroupsData) != 1 || rv.ReviewGroupsData[0].Group != rvAdminGroup ||
			rv.ReviewGroupsData[0].Status != "PENDING":
			c.Fatalf("filed review groups=%+v, want one PENDING %q group", rv.ReviewGroupsData, rvAdminGroup)
		}

		st := f.status(c, rv.ID)
		if st.Status != "PENDING" || st.ListenerName != rvLane || st.ApprovalRule != f.rule || st.DecidedAt != nil {
			c.Fatalf("sidecar status read = %+v, want PENDING on %s under %s with no decided_at", st, rvLane, f.rule)
		}
		var human struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		c.Admin().Do(c, http.MethodGet, "/reviews/"+rv.ID, nil).Expect(c, http.StatusOK).JSON(c, &human)
		if human.ID != rv.ID || human.Status != "PENDING" {
			c.Fatalf("reviewer reads review %s as %+v, want it PENDING", rv.ID, human)
		}
	},
})

var _ = register(Check{
	ID:    "RV-03",
	Title: "resending a held statement answers the review already filed for it; another statement files its own",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		f := rvNewReviewing(c)
		first := f.file(c, "UPDATE accounts SET balance = 0")
		code, again := f.send(c, "UPDATE accounts SET balance = 0")
		if code != http.StatusOK {
			c.Fatalf("resend answered %d, want 200", code)
		}
		rvWantAnswer(c, "resend while pending", again, false, "PENDING", first.Review.ID)

		other := f.file(c, "UPDATE accounts SET balance = 1")
		if other.Review.ID == first.Review.ID {
			c.Fatalf("a different statement was answered with review %s", first.Review.ID)
		}
		_, third := f.send(c, "UPDATE accounts SET balance = 0")
		rvWantAnswer(c, "resend after another statement", third, false, "PENDING", first.Review.ID)
	},
})

var _ = register(Check{
	ID:    "RV-04",
	Title: "an approved review releases its statement exactly once on resend",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		f := rvNewReviewing(c)
		const stmt = "DROP TABLE staging_orders"
		filed := f.file(c, stmt)
		rvApprove(c, filed.Review.ID)

		code, released := f.send(c, stmt)
		if code != http.StatusOK {
			c.Fatalf("resend after approval answered %d, want 200", code)
		}
		rvWantAnswer(c, "first resend after approval", released, true, "EXECUTED", filed.Review.ID)

		code, spent := f.send(c, stmt)
		if code != http.StatusCreated || spent.Forward || spent.Review.Status != "PENDING" || spent.Review.ID == filed.Review.ID {
			c.Fatalf("second resend: status=%d forward=%v review=%s %s, want 201 forward=false and a new PENDING review",
				code, spent.Forward, spent.Review.ID, spent.Review.Status)
		}
		if st := f.status(c, filed.Review.ID); st.Status != "EXECUTED" {
			c.Fatalf("spent review reads %s, want EXECUTED", st.Status)
		}
	},
})

var _ = register(Check{
	ID:    "RV-05",
	Title: "a waiting sidecar claims its review by id: pending waits, approved releases once, reads never consume",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		f := rvNewReviewing(c)
		filed := f.file(c, "TRUNCATE audit_log")
		id := filed.Review.ID

		rvWantAnswer(c, "claim while pending", f.claim(c, id), false, "PENDING", id)
		rvApprove(c, id)
		for i := 0; i < 2; i++ {
			st := f.status(c, id)
			if st.Status != "APPROVED" || st.DecidedAt == nil {
				c.Fatalf("status read %d after approval = %+v, want APPROVED with decided_at", i+1, st)
			}
		}
		rvWantAnswer(c, "first claim after approval", f.claim(c, id), true, "EXECUTED", id)
		rvWantAnswer(c, "second claim", f.claim(c, id), false, "EXECUTED", id)

		sc := c.Sidecar(f.sc.Token)
		sc.Do(c, http.MethodPost, "/sidecars/reviews/not-a-uuid/claim", nil).Expect(c, http.StatusNotFound)
		sc.Do(c, http.MethodPost, "/sidecars/reviews/9f97c0de-0000-4000-8000-000000000001/claim", nil).Expect(c, http.StatusNotFound)
		sc.Do(c, http.MethodGet, "/sidecars/reviews/9f97c0de-0000-4000-8000-000000000001", nil).Expect(c, http.StatusNotFound)
	},
})

var _ = register(Check{
	ID:    "RV-06",
	Title: "a rejected review never releases its statement and keeps the reviewer's reason",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		f := rvNewReviewing(c)
		const stmt = "DELETE FROM customers"
		filed := f.file(c, stmt)
		id := filed.Review.ID
		rvDecide(c, id, map[string]any{"status": "REJECTED", "rejection_reason": "not during business hours"}).
			Expect(c, http.StatusOK)

		rvWantAnswer(c, "claim after rejection", f.claim(c, id), false, "REJECTED", id)
		st := f.status(c, id)
		if st.Status != "REJECTED" || st.DecidedAt == nil || st.RejectionReason == nil ||
			*st.RejectionReason != "not during business hours" {
			c.Fatalf("rejected review reads %+v, want REJECTED with decided_at and the reason", st)
		}
		rvDecide(c, id, map[string]any{"status": "APPROVED"}).Expect(c, http.StatusBadRequest)
		rvWantAnswer(c, "claim after a late approval attempt", f.claim(c, id), false, "REJECTED", id)

		code, resent := f.send(c, stmt)
		if code != http.StatusCreated || resent.Forward || resent.Review.ID == id || resent.Review.Status != "PENDING" {
			c.Fatalf("resend after rejection: status=%d forward=%v review=%s %s, want 201 and a new PENDING review",
				code, resent.Forward, resent.Review.ID, resent.Review.Status)
		}
	},
})

var _ = register(Check{
	ID:    "RV-07",
	Title: "revoking an approval before the sidecar claims it withholds the statement; a resend files a new review",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		f := rvNewReviewing(c)
		const stmt = "ALTER TABLE invoices DROP COLUMN total"
		pending := f.file(c, "ALTER TABLE invoices ADD COLUMN note text")
		rvDecide(c, pending.Review.ID, map[string]any{"status": "REVOKED"}).Expect(c, http.StatusBadRequest)

		filed := f.file(c, stmt)
		id := filed.Review.ID
		rvApprove(c, id)
		var rev struct {
			Status string `json:"status"`
		}
		rvDecide(c, id, map[string]any{"status": "REVOKED"}).Expect(c, http.StatusOK).JSON(c, &rev)
		if rev.Status != "REVOKED" {
			c.Fatalf("revoking review %s left it %s", id, rev.Status)
		}
		rvWantAnswer(c, "claim after revoke", f.claim(c, id), false, "REVOKED", id)

		code, resent := f.send(c, stmt)
		if code != http.StatusCreated || resent.Forward || resent.Review.ID == id || resent.Review.Status != "PENDING" {
			c.Fatalf("resend after revoke: status=%d forward=%v review=%s %s, want 201 and a new PENDING review",
				code, resent.Forward, resent.Review.ID, resent.Review.Status)
		}
	},
})

var _ = register(Check{
	ID:    "RV-08",
	Title: "a sidecar lists its reviews newest first, filtered by status and bounded by limit",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		f := rvNewReviewing(c)
		a := f.file(c, "SELECT 'list-a'").Review.ID
		b := f.file(c, "SELECT 'list-b'").Review.ID
		d := f.file(c, "SELECT 'list-c'").Review.ID
		rvApprove(c, a)
		rvDecide(c, b, map[string]any{"status": "REJECTED"}).Expect(c, http.StatusOK)

		list := func(query string) []rvStatus {
			var out []rvStatus
			c.Sidecar(f.sc.Token).Do(c, http.MethodGet, "/sidecars/reviews"+query, nil).Expect(c, http.StatusOK).JSON(c, &out)
			return out
		}
		ids := func(l []rvStatus) string {
			s := make([]string, 0, len(l))
			for _, r := range l {
				s = append(s, r.ID+":"+r.Status)
			}
			return strings.Join(s, ",")
		}
		if all := list(""); len(all) != 3 || all[0].ID != d || all[1].ID != b || all[2].ID != a {
			c.Fatalf("list = [%s], want newest first [%s,%s,%s]", ids(all), d, b, a)
		}
		if got := list("?status=approved"); len(got) != 1 || got[0].ID != a || got[0].Status != "APPROVED" {
			c.Fatalf("?status=approved = [%s], want only %s", ids(got), a)
		}
		if got := list("?status=PENDING"); len(got) != 1 || got[0].ID != d {
			c.Fatalf("?status=PENDING = [%s], want only %s", ids(got), d)
		}
		if got := list("?status=REJECTED"); len(got) != 1 || got[0].ID != b {
			c.Fatalf("?status=REJECTED = [%s], want only %s", ids(got), b)
		}
		if got := list("?limit=1"); len(got) != 1 || got[0].ID != d {
			c.Fatalf("?limit=1 = [%s], want only the newest %s", ids(got), d)
		}
		if got := list("?status=approved"); len(got) != 1 || got[0].Status != "APPROVED" {
			c.Fatalf("listing consumed the approval: [%s]", ids(got))
		}
		sc := c.Sidecar(f.sc.Token)
		for _, q := range []string{"?status=bogus", "?limit=0", "?limit=201", "?limit=x"} {
			sc.Do(c, http.MethodGet, "/sidecars/reviews"+q, nil).Expect(c, http.StatusBadRequest)
		}
	},
})

var _ = register(Check{
	ID:    "RV-09",
	Title: "a sidecar token reaches only the reviews that sidecar filed and the rules its own listeners name",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		a := rvNewReviewing(c)
		ruleB := rvCreateApprovalRule(c, "approvers-b", nil)
		b := rvCreateSidecar(c, "sc-b", rvConfig(rvListener(rvLane, 15432, rvHold(ruleB))))

		filed := a.file(c, "DELETE FROM sessions")
		rvApprove(c, filed.Review.ID)

		other := c.Sidecar(b.Token)
		other.Do(c, http.MethodGet, "/sidecars/reviews/"+filed.Review.ID, nil).Expect(c, http.StatusNotFound)
		other.Do(c, http.MethodPost, "/sidecars/reviews/"+filed.Review.ID+"/claim", nil).Expect(c, http.StatusNotFound)
		var listed []rvStatus
		other.Do(c, http.MethodGet, "/sidecars/reviews", nil).Expect(c, http.StatusOK).JSON(c, &listed)
		if len(listed) != 0 {
			c.Fatalf("sidecar B lists %d reviews it never filed", len(listed))
		}
		// Same listener name, A's rule: B's served config does not name it.
		other.Do(c, http.MethodPost, "/sidecars/reviews", rvReviewBody(rvLane, a.rule, "DELETE FROM sessions")).
			Expect(c, http.StatusUnprocessableEntity)

		if st := a.status(c, filed.Review.ID); st.Status != "APPROVED" {
			c.Fatalf("A's approval reads %s after B's attempts, want APPROVED", st.Status)
		}
		rvWantAnswer(c, "A claims its own approval", a.claim(c, filed.Review.ID), true, "EXECUTED", filed.Review.ID)
	},
})

var _ = register(Check{
	ID:    "RV-10",
	Title: "a review request the served config does not authorize, or that is malformed, files nothing",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		f := rvNewReviewing(c)
		unbound := rvCreateApprovalRule(c, "unbound", nil)
		sc := c.Sidecar(f.sc.Token)
		const stmt = "DELETE FROM orders"
		for what, body := range map[string]map[string]string{
			"listener with no hold":    rvReviewBody(rvOtherLane, f.rule, stmt),
			"rule the lane not names":  rvReviewBody(rvLane, unbound, stmt),
			"rule that does not exist": rvReviewBody(rvLane, c.Name("missing"), stmt),
			"unknown listener":         rvReviewBody("nope", f.rule, stmt),
		} {
			r := sc.Do(c, http.MethodPost, "/sidecars/reviews", body)
			if r.Status != http.StatusUnprocessableEntity {
				c.Fatalf("%s: status %d, want 422: %s", what, r.Status, truncate(r.Body))
			}
			c.Logf("%s: 422 %s", what, truncate(r.Body))
		}
		sc.Do(c, http.MethodPost, "/sidecars/reviews", map[string]string{
			"listener_name": rvLane, "approval_rule": f.rule, "payload": "!!!not-base64!!!",
		}).Expect(c, http.StatusBadRequest)
		sc.Do(c, http.MethodPost, "/sidecars/reviews", map[string]string{
			"listener_name": rvLane, "payload": base64.StdEncoding.EncodeToString([]byte(stmt)),
		}).Expect(c, http.StatusBadRequest)
		sc.Do(c, http.MethodPost, "/sidecars/reviews", rvReviewBody(rvLane, f.rule, strings.Repeat("x", 100001))).
			Expect(c, http.StatusRequestEntityTooLarge)

		var listed []rvStatus
		sc.Do(c, http.MethodGet, "/sidecars/reviews", nil).Expect(c, http.StatusOK).JSON(c, &listed)
		if len(listed) != 0 {
			c.Fatalf("refused requests filed %d reviews", len(listed))
		}
	},
})

var _ = register(Check{
	ID:    "RV-11",
	Title: "sidecar review routes refuse a caller without a valid sidecar token",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		const id = "9f97c0de-0000-4000-8000-000000000002"
		body := rvReviewBody(rvLane, "any-rule", "SELECT 1")
		admin := c.Admin()
		for _, cl := range []struct {
			who string
			cl  *Client
		}{{"anonymous", c.Anonymous()}, {"unknown token", c.Sidecar("hsc_parity_not_a_token")}, {"admin bearer", admin}} {
			for _, call := range []struct {
				method, path string
				body         any
			}{
				{http.MethodPost, "/sidecars/reviews", body},
				{http.MethodGet, "/sidecars/reviews", nil},
				{http.MethodGet, "/sidecars/reviews/" + id, nil},
				{http.MethodPost, "/sidecars/reviews/" + id + "/claim", nil},
			} {
				if r := cl.cl.Do(c, call.method, call.path, call.body); r.Status != http.StatusUnauthorized {
					c.Fatalf("%s %s as %s: status %d, want 401: %s", call.method, call.path, cl.who, r.Status, truncate(r.Body))
				}
			}
		}
	},
})

var _ = register(Check{
	ID:    "RV-12",
	Title: "a review settles by the policy of its rule: all groups, a minimum, or a force approval",
	Runs:  rvReviewRuns,
	Fn: func(c *C) {
		all := rvCreateApprovalRule(c, "all-groups", map[string]any{
			"reviewers_groups": []string{rvAdminGroup, "dba"}, "all_groups_must_approve": true, "min_approvals": nil,
		})
		minOne := rvCreateApprovalRule(c, "min-one", map[string]any{
			"reviewers_groups": []string{rvAdminGroup, "dba"}, "min_approvals": 1,
		})
		force := rvCreateApprovalRule(c, "force", map[string]any{
			"reviewers_groups": []string{"dba"}, "force_approval_groups": []string{rvAdminGroup}, "min_approvals": 1,
		})
		sc := rvCreateSidecar(c, "sc", rvConfig(
			rvListener("ledger", 15432, rvHold(all)),
			rvListener("billing", 15433, rvHold(minOne)),
			rvListener("vault", 15434, rvHold(force)),
		))
		on := func(lane, rule string) rvReviewing { return rvReviewing{sc: sc, rule: rule, lane: lane} }

		ledger := on("ledger", all)
		r := ledger.file(c, "DELETE FROM ledger")
		resp := rvDecide(c, r.Review.ID, map[string]any{"status": "APPROVED"}).Expect(c, http.StatusOK)
		var partial struct {
			Status string `json:"status"`
		}
		resp.JSON(c, &partial)
		if partial.Status != "PENDING" {
			c.Fatalf("all-groups review after one of two groups approved = %s, want PENDING", partial.Status)
		}
		rvWantAnswer(c, "claim of a half-approved all-groups review", ledger.claim(c, r.Review.ID), false, "PENDING", r.Review.ID)

		billing := on("billing", minOne)
		r = billing.file(c, "DELETE FROM billing")
		if len(r.Review.ReviewGroupsData) != 2 {
			c.Fatalf("min-one review has %d groups, want 2", len(r.Review.ReviewGroupsData))
		}
		rvDecide(c, r.Review.ID, map[string]any{"status": "APPROVED"}).Expect(c, http.StatusOK)
		rvWantAnswer(c, "claim of a min-one review after one approval", billing.claim(c, r.Review.ID), true, "EXECUTED", r.Review.ID)

		vault := on("vault", force)
		r = vault.file(c, "DELETE FROM vault")
		if len(r.Review.ReviewGroupsData) != 1 || r.Review.ReviewGroupsData[0].Group != "dba" {
			c.Fatalf("force rule review groups = %+v, want only dba", r.Review.ReviewGroupsData)
		}
		rvDecide(c, r.Review.ID, map[string]any{"status": "APPROVED", "force_review": true}).Expect(c, http.StatusOK)
		rvWantAnswer(c, "claim after a force approval", vault.claim(c, r.Review.ID), true, "EXECUTED", r.Review.ID)
	},
})

// Rules bound to sidecar listeners.

type rvRuleTarget struct {
	SidecarID    string `json:"sidecar_id"`
	ListenerName string `json:"listener_name,omitempty"`
}

func rvTargetsOn(sc rvSidecar, lanes ...string) []rvRuleTarget {
	out := make([]rvRuleTarget, 0, len(lanes))
	for _, l := range lanes {
		out = append(out, rvRuleTarget{SidecarID: sc.ID, ListenerName: l})
	}
	return out
}

func rvWantTargets(c *C, what string, got []rvRuleTarget, sc rvSidecar, lanes ...string) {
	if len(got) != len(lanes) {
		c.Fatalf("%s: sidecar_targets=%+v, want %v on %s", what, got, lanes, sc.ID)
	}
	for i, l := range lanes {
		if got[i].SidecarID != sc.ID || got[i].ListenerName != l {
			c.Fatalf("%s: sidecar_targets=%+v, want %v on %s", what, got, lanes, sc.ID)
		}
	}
}

// rvTwoLaneSidecar has two plain postgres lanes and the analyzer section.
func rvTwoLaneSidecar(c *C) rvSidecar {
	return rvCreateSidecar(c, "sc", rvConfig(rvListener(rvLane, 15432, nil), rvListener(rvOtherLane, 15433, nil)))
}

func rvGuardrailSpec(name string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"rules":[{"name":%q,"type":"operation","operations":["drop","truncate"],"message":"ask the data team"}]}`, name))
}

type rvGuardrailResp struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	SidecarSpec    json.RawMessage `json:"sidecar_spec"`
	SidecarTargets []rvRuleTarget  `json:"sidecar_targets"`
}

func rvCreateBoundGuardrail(c *C, sc rvSidecar, name string) rvGuardrailResp {
	var g rvGuardrailResp
	c.Admin().Do(c, http.MethodPost, "/guardrails", map[string]any{
		"name": name, "description": "parity", "sidecar_spec": rvGuardrailSpec(name),
		"sidecar_targets": rvTargetsOn(sc, rvLane),
	}).Expect(c, http.StatusCreated).JSON(c, &g)
	rvWantTargets(c, "guardrail create", g.SidecarTargets, sc, rvLane)
	return g
}

var _ = register(Check{
	ID:    "RUL-01",
	Title: "a guardrail bound to one listener is served on that listener only",
	Runs:  rvRuleRuns,
	Fn: func(c *C) {
		sc := rvTwoLaneSidecar(c)
		name := c.Name("no-drop")
		g := rvCreateBoundGuardrail(c, sc, name)

		var read rvGuardrailResp
		c.Admin().Do(c, http.MethodGet, "/guardrails/"+g.ID, nil).Expect(c, http.StatusOK).JSON(c, &read)
		rvWantTargets(c, "guardrail read", read.SidecarTargets, sc, rvLane)
		if len(read.SidecarSpec) == 0 || !strings.Contains(string(read.SidecarSpec), `"operation"`) {
			c.Fatalf("guardrail read returns sidecar_spec %s, want the stored spec", read.SidecarSpec)
		}

		cfg, _ := rvHandshake(c, sc.Token)
		lane := rvServedLane(c, cfg, rvLane)
		if !rvLaneHasGuardrail(lane, name) {
			c.Fatalf("served %s guardrails = %+v, want rule %q", rvLane, lane.Guardrails, name)
		}
		for _, r := range lane.Guardrails.Rules {
			if r.Name == name && (string(r.Type) != "operation" || len(r.Operations) != 2) {
				c.Fatalf("served rule %q = %+v, want the operation rule as authored", name, r)
			}
		}
		if rvLaneHasGuardrail(rvServedLane(c, cfg, rvOtherLane), name) {
			c.Fatalf("rule %q is served on %s, which it is not bound to", name, rvOtherLane)
		}
		if cfg.Guardrails != nil && len(cfg.Guardrails.Rules) > 0 {
			c.Fatalf("a listener binding wrote top-level guardrails: %+v", cfg.Guardrails)
		}
	},
})

var _ = register(Check{
	ID:    "RUL-02",
	Title: "a data masking rule with a sidecar spec saves without a DLP provider and is served on its listener",
	Runs:  rvRuleRuns,
	Fn: func(c *C) {
		sc := rvTwoLaneSidecar(c)
		name := c.Name("mask")
		var created struct {
			ID             string         `json:"id"`
			SidecarTargets []rvRuleTarget `json:"sidecar_targets"`
		}
		c.Admin().Do(c, http.MethodPost, "/datamasking-rules", map[string]any{
			"name": name, "description": "parity",
			"sidecar_spec":    json.RawMessage(`{"rules":[{"name":"email-column","columns":["email"],"strategy":"hash"}]}`),
			"sidecar_targets": rvTargetsOn(sc, rvLane),
		}).Expect(c, http.StatusCreated).JSON(c, &created)
		rvWantTargets(c, "masking create", created.SidecarTargets, sc, rvLane)

		cfg, _ := rvHandshake(c, sc.Token)
		lane := rvServedLane(c, cfg, rvLane)
		if lane.Mask == nil {
			c.Fatalf("served %s has no mask block", rvLane)
		}
		var rules []struct {
			Name     string   `json:"name"`
			Columns  []string `json:"columns"`
			Strategy string   `json:"strategy"`
		}
		if err := json.Unmarshal(lane.Mask.Rules, &rules); err != nil {
			c.Fatalf("served mask rules are not a list: %v (%s)", err, lane.Mask.Rules)
		}
		if len(rules) != 1 || rules[0].Name != "email-column" || rules[0].Strategy != "hash" ||
			len(rules[0].Columns) != 1 || rules[0].Columns[0] != "email" {
			c.Fatalf("served mask rules = %+v, want the hash rule on column email", rules)
		}
		if other := rvServedLane(c, cfg, rvOtherLane); other.Mask != nil {
			c.Fatalf("served %s carries a mask block nobody bound: %s", rvOtherLane, other.Mask.Rules)
		}
	},
})

func rvAnalyzerRuleBody(name string, spec string, targets []rvRuleTarget) map[string]any {
	body := map[string]any{
		"name": name, "connection_names": []string{},
		"risk_evaluation": map[string]any{
			"low_risk_action": "allow_execution", "medium_risk_action": "allow_execution", "high_risk_action": "block_execution",
		},
		"sidecar_spec": json.RawMessage(spec),
	}
	if targets != nil {
		body["sidecar_targets"] = targets
	}
	return body
}

var _ = register(Check{
	ID:    "RUL-03",
	Title: "an analyzer rule bound to a listener serves its trigger, verdict actions and prompt there",
	Runs:  rvRuleRuns,
	Fn: func(c *C) {
		sc := rvTwoLaneSidecar(c)
		name := c.Name("risky")
		const prompt = "Treat the payments schema as high risk."
		spec := fmt.Sprintf(`{"trigger":{"operations":["update","delete"]},"high":"block","medium":"warn","prompt":%q}`, prompt)
		var created struct {
			SidecarTargets []rvRuleTarget `json:"sidecar_targets"`
		}
		c.Admin().Do(c, http.MethodPost, "/ai/session-analyzer/rules", rvAnalyzerRuleBody(name, spec, rvTargetsOn(sc, rvLane))).
			Expect(c, http.StatusCreated).JSON(c, &created)
		rvWantTargets(c, "analyzer create", created.SidecarTargets, sc, rvLane)

		var read struct {
			SidecarSpec    json.RawMessage `json:"sidecar_spec"`
			SidecarTargets []rvRuleTarget  `json:"sidecar_targets"`
		}
		c.Admin().Do(c, http.MethodGet, "/ai/session-analyzer/rules/"+name, nil).Expect(c, http.StatusOK).JSON(c, &read)
		rvWantTargets(c, "analyzer read", read.SidecarTargets, sc, rvLane)

		cfg, _ := rvHandshake(c, sc.Token)
		lane := rvServedLane(c, cfg, rvLane)
		a := lane.Analyzer
		if a == nil || a.HighRisk != "block" || a.MediumRisk != "warn" || a.Prompt != prompt ||
			a.Trigger == nil || len(a.Trigger.Operations) != 2 {
			c.Fatalf("served %s analyzer = %+v, want the bound rule's trigger, actions and prompt", rvLane, a)
		}
		if other := rvServedLane(c, cfg, rvOtherLane); other.Analyzer != nil {
			c.Fatalf("served %s carries an analyzer block nobody bound: %+v", rvOtherLane, other.Analyzer)
		}
	},
})

var _ = register(Check{
	ID:    "RUL-04",
	Title: "an analyzer rule that holds statements creates its approval rule, and reviews follow its binding",
	Runs:  rvRuleRuns,
	Fn: func(c *C) {
		sc := rvTwoLaneSidecar(c)
		name := c.Name("hold")
		spec := fmt.Sprintf(`{"high":"require_review","approval_rule":%q}`, name)
		body := rvAnalyzerRuleBody(name, spec, rvTargetsOn(sc, rvLane))
		body["reviewers_groups"] = []string{rvAdminGroup}
		var created struct {
			ReviewersGroups []string `json:"reviewers_groups"`
		}
		c.Admin().Do(c, http.MethodPost, "/ai/session-analyzer/rules", body).Expect(c, http.StatusCreated).JSON(c, &created)
		if len(created.ReviewersGroups) != 1 || created.ReviewersGroups[0] != rvAdminGroup {
			c.Fatalf("analyzer rule reviewers_groups=%v, want [%s]", created.ReviewersGroups, rvAdminGroup)
		}
		var approval rvAccessRule
		c.Admin().Do(c, http.MethodGet, "/access-requests/rules/"+name, nil).Expect(c, http.StatusOK).JSON(c, &approval)
		if approval.AccessType != "sidecar" || approval.ManagedBy == nil ||
			len(approval.ReviewersGroups) != 1 || approval.ReviewersGroups[0] != rvAdminGroup {
			c.Fatalf("approval rule the hold created = %+v, want a managed sidecar rule reviewed by %s", approval, rvAdminGroup)
		}

		cfg, rev := rvHandshake(c, sc.Token)
		lane := rvServedLane(c, cfg, rvLane)
		if lane.Analyzer == nil || lane.Analyzer.ApprovalRule != name || lane.Analyzer.HighRisk != "require_review" {
			c.Fatalf("served %s analyzer = %+v, want the hold under %q", rvLane, lane.Analyzer, name)
		}
		// Authorized by the COMPOSED document: the stored listener names no rule.
		f := rvReviewing{sc: sc, rule: name, lane: rvLane}
		filed := f.file(c, "DELETE FROM payouts")
		rvApprove(c, filed.Review.ID)
		rvWantAnswer(c, "claim under a bound hold", f.claim(c, filed.Review.ID), true, "EXECUTED", filed.Review.ID)

		unbind := rvAnalyzerRuleBody(name, spec, []rvRuleTarget{})
		c.Admin().Do(c, http.MethodPut, "/ai/session-analyzer/rules/"+name, unbind).Expect(c, http.StatusOK)
		cfg, rev2 := rvHandshake(c, sc.Token)
		if a := rvServedLane(c, cfg, rvLane).Analyzer; a != nil {
			c.Fatalf("served %s still carries the unbound hold: %+v", rvLane, a)
		}
		if rev2 == rev {
			c.Fatalf("revision %s did not change when the hold was unbound", rev)
		}
		c.Sidecar(sc.Token).Do(c, http.MethodPost, "/sidecars/reviews", rvReviewBody(rvLane, name, "DELETE FROM payouts_2")).
			Expect(c, http.StatusUnprocessableEntity)
	},
})

var _ = register(Check{
	ID:    "RUL-05",
	Title: "unbinding a rule with sidecar_targets [] removes it from the served config and moves the revision",
	Runs:  rvRuleRuns,
	Fn: func(c *C) {
		sc := rvTwoLaneSidecar(c)
		_, before := rvHandshake(c, sc.Token)
		name := c.Name("unbind")
		g := rvCreateBoundGuardrail(c, sc, name)

		cfg, bound := rvHandshake(c, sc.Token)
		if !rvLaneHasGuardrail(rvServedLane(c, cfg, rvLane), name) {
			c.Fatalf("bound rule %q is not served", name)
		}
		if bound == before {
			c.Fatalf("revision %s did not change when a rule was bound", bound)
		}
		if _, again := rvHandshake(c, sc.Token); again != bound {
			c.Fatalf("revision moved from %s to %s with nothing changed", bound, again)
		}

		var put rvGuardrailResp
		c.Admin().Do(c, http.MethodPut, "/guardrails/"+g.ID, map[string]any{
			"name": name, "description": "parity", "sidecar_targets": []rvRuleTarget{},
		}).Expect(c, http.StatusOK).JSON(c, &put)
		if len(put.SidecarTargets) != 0 {
			c.Fatalf("unbind answered sidecar_targets=%+v, want none", put.SidecarTargets)
		}
		cfg, unbound := rvHandshake(c, sc.Token)
		if rvLaneHasGuardrail(rvServedLane(c, cfg, rvLane), name) {
			c.Fatalf("unbound rule %q is still served", name)
		}
		if unbound == bound {
			c.Fatalf("revision %s did not change when the rule was unbound", unbound)
		}
		var read rvGuardrailResp
		c.Admin().Do(c, http.MethodGet, "/guardrails/"+g.ID, nil).Expect(c, http.StatusOK).JSON(c, &read)
		if len(read.SidecarTargets) != 0 || !strings.Contains(string(read.SidecarSpec), `"operation"`) {
			c.Fatalf("unbound rule reads targets=%+v spec=%s, want no targets and the spec kept", read.SidecarTargets, read.SidecarSpec)
		}
	},
})

var _ = register(Check{
	ID:    "RUL-06",
	Title: "an edit that omits sidecar_spec and sidecar_targets keeps the rule bound and served",
	Runs:  rvRuleRuns,
	Fn: func(c *C) {
		sc := rvTwoLaneSidecar(c)
		name := c.Name("keep")
		g := rvCreateBoundGuardrail(c, sc, name)

		var put rvGuardrailResp
		c.Admin().Do(c, http.MethodPut, "/guardrails/"+g.ID, map[string]any{
			"name": name, "description": "edited from a form that knows nothing about sidecars",
		}).Expect(c, http.StatusOK).JSON(c, &put)
		rvWantTargets(c, "edit without sidecar fields", put.SidecarTargets, sc, rvLane)
		if !strings.Contains(string(put.SidecarSpec), `"operation"`) {
			c.Fatalf("edit without sidecar fields answered sidecar_spec %s, want the stored spec", put.SidecarSpec)
		}
		cfg, _ := rvHandshake(c, sc.Token)
		if !rvLaneHasGuardrail(rvServedLane(c, cfg, rvLane), name) {
			c.Fatalf("rule %q is no longer served after an edit that did not mention sidecars", name)
		}
	},
})

var _ = register(Check{
	ID:    "RUL-07",
	Title: "a binding the sidecar could not run is refused at save and stores nothing",
	Runs:  rvRuleRuns,
	Fn: func(c *C) {
		sc := rvTwoLaneSidecar(c)
		bare := rvCreateSidecar(c, "bare", map[string]any{"listeners": []map[string]any{rvListener(rvLane, 15432, nil)}})
		name := c.Name("refused")
		admin := c.Admin()
		refuse := func(what, path string, body map[string]any) {
			r := admin.Do(c, http.MethodPost, path, body)
			if r.Status != http.StatusUnprocessableEntity {
				c.Fatalf("%s: status %d, want 422: %s", what, r.Status, truncate(r.Body))
			}
			c.Logf("%s: 422 %s", what, truncate(r.Body))
		}
		refuse("guardrail on an unknown listener", "/guardrails", map[string]any{
			"name": name, "sidecar_spec": rvGuardrailSpec(name), "sidecar_targets": rvTargetsOn(sc, "nope"),
		})
		refuse("guardrail on a whole sidecar", "/guardrails", map[string]any{
			"name": name, "sidecar_spec": rvGuardrailSpec(name), "sidecar_targets": []rvRuleTarget{{SidecarID: sc.ID}},
		})
		refuse("guardrail with no rules", "/guardrails", map[string]any{
			"name": name, "sidecar_spec": json.RawMessage(`{"rules":[]}`), "sidecar_targets": rvTargetsOn(sc, rvLane),
		})
		refuse("analyzer on a sidecar with no analyzer section", "/ai/session-analyzer/rules",
			rvAnalyzerRuleBody(name, `{"high":"block"}`, rvTargetsOn(bare, rvLane)))
		refuse("hold under an approval rule that does not exist", "/ai/session-analyzer/rules",
			rvAnalyzerRuleBody(name, fmt.Sprintf(`{"high":"require_review","approval_rule":%q}`, c.Name("missing")), rvTargetsOn(sc, rvLane)))

		admin.Do(c, http.MethodGet, "/ai/session-analyzer/rules/"+name, nil).Expect(c, http.StatusNotFound)
		rvCreateBoundGuardrail(c, sc, name)
		cfg, _ := rvHandshake(c, sc.Token)
		if !rvLaneHasGuardrail(rvServedLane(c, cfg, rvLane), name) {
			c.Fatalf("rule %q saved after the refusals is not served", name)
		}
	},
})
