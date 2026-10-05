//go:build integration && parity

package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// What today's gateway customers do through the real agent. Every one of
// these has to keep working with beta.sidecar_listeners on.
var gatewayRuns = map[Run]Expect{GatewayFlagOff: Must, GatewayFlagOn: Must}

type gwConnection struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	SubType        string            `json:"subtype"`
	AgentID        string            `json:"agent_id"`
	Status         string            `json:"status"`
	ManagedBy      *string           `json:"managed_by"`
	Command        []string          `json:"command"`
	Reviewers      []string          `json:"reviewers"`
	ConnectionTags map[string]string `json:"connection_tags"`
	AccessModeRun  string            `json:"access_mode_runbooks"`
	AccessModeExec string            `json:"access_mode_exec"`
}

type gwExecResult struct {
	HasReview    bool   `json:"has_review"`
	SessionID    string `json:"session_id"`
	Output       string `json:"output"`
	OutputStatus string `json:"output_status"`
	ExitCode     int    `json:"exit_code"`
}

type gwReview struct {
	ID                    string  `json:"id"`
	Session               string  `json:"session"`
	Status                string  `json:"status"`
	AccessRequestRuleName *string `json:"access_request_rule_name"`
	ReviewGroupsData      []struct {
		Group  string `json:"group"`
		Status string `json:"status"`
	} `json:"review_groups_data"`
}

// gwBashBody is a custom /bin/bash connection on the run's agent, the
// shape the webapp saves.
func gwBashBody(c *C, name string, reviewers []string) map[string]any {
	if c.AgentID == "" {
		c.Fatalf("no agent is connected on run %s", c.Run)
	}
	if reviewers == nil {
		reviewers = []string{}
	}
	return map[string]any{
		"name":                 name,
		"type":                 "custom",
		"subtype":              "custom",
		"agent_id":             c.AgentID,
		"command":              []string{"/bin/bash"},
		"secret":               map[string]any{},
		"reviewers":            reviewers,
		"access_mode_runbooks": "enabled",
		"access_mode_exec":     "enabled",
		"access_mode_connect":  "enabled",
		"access_schema":        "disabled",
	}
}

func gwCreateBash(c *C, suffix string, reviewers []string) gwConnection {
	var conn gwConnection
	c.Admin().Do(c, http.MethodPost, "/connections", gwBashBody(c, c.Name(suffix), reviewers)).
		Expect(c, http.StatusCreated).JSON(c, &conn)
	if conn.ID == "" || conn.Name != c.Name(suffix) {
		c.Fatalf("created connection id=%q name=%q, want an id and name %q", conn.ID, conn.Name, c.Name(suffix))
	}
	return conn
}

func gwGetConnection(c *C, name string) gwConnection {
	var conn gwConnection
	c.Admin().Do(c, http.MethodGet, "/connections/"+name, nil).Expect(c, http.StatusOK).JSON(c, &conn)
	return conn
}

func gwListConnections(c *C, query string) []gwConnection {
	var list []gwConnection
	c.Admin().Do(c, http.MethodGet, "/connections"+query, nil).Expect(c, http.StatusOK).JSON(c, &list)
	return list
}

func gwFindConnection(list []gwConnection, name string) *gwConnection {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// gwExec runs script through POST /sessions, the API exec the webapp editor
// and the CLI use.
func gwExec(c *C, conn, script string) gwExecResult {
	var res gwExecResult
	c.Admin().Do(c, http.MethodPost, "/sessions", map[string]string{"connection": conn, "script": script}).
		Expect(c, http.StatusOK).JSON(c, &res)
	if res.SessionID == "" {
		c.Fatalf("exec on %s answered no session_id: %+v", conn, res)
	}
	return res
}

// gwExecHeld runs script on a connection that requires a review and returns
// the held session id.
func gwExecHeld(c *C, conn, script, marker string) string {
	res := gwExec(c, conn, script)
	if !res.HasReview {
		c.Fatalf("exec on %s ran without review: %+v", conn, res)
	}
	if strings.Contains(res.Output, marker) {
		c.Fatalf("exec held for review but the command ran: output=%q", res.Output)
	}
	return res.SessionID
}

func gwGetReview(c *C, idOrSid string) gwReview {
	var rev gwReview
	c.Admin().Do(c, http.MethodGet, "/reviews/"+idOrSid, nil).Expect(c, http.StatusOK).JSON(c, &rev)
	return rev
}

func gwDecide(c *C, reviewID, status string) gwReview {
	var rev gwReview
	c.Admin().Do(c, http.MethodPut, "/reviews/"+reviewID, map[string]string{"status": status}).
		Expect(c, http.StatusOK).JSON(c, &rev)
	return rev
}

// gwRunReviewed runs a reviewed session through POST /sessions/:id/exec.
func gwRunReviewed(c *C, sid string) Resp {
	return c.Admin().Do(c, http.MethodPost, "/sessions/"+sid+"/exec", nil)
}

func gwPendingAdminReview(c *C, sid string) gwReview {
	rev := gwGetReview(c, sid)
	if rev.Session != sid || rev.Status != "PENDING" {
		c.Fatalf("review of session %s: session=%q status=%q, want PENDING", sid, rev.Session, rev.Status)
	}
	if len(rev.ReviewGroupsData) != 1 || rev.ReviewGroupsData[0].Group != "admin" ||
		rev.ReviewGroupsData[0].Status != "PENDING" {
		c.Fatalf("review groups = %+v, want one PENDING group admin", rev.ReviewGroupsData)
	}
	return rev
}

// gwRunApproved runs an approved session and checks it executed once.
func gwRunApproved(c *C, sid, reviewID, marker string) {
	var res gwExecResult
	gwRunReviewed(c, sid).Expect(c, http.StatusOK).JSON(c, &res)
	if res.HasReview || !strings.Contains(res.Output, marker) || res.ExitCode != 0 {
		c.Fatalf("approved session did not run: %+v", res)
	}
	if got := gwGetReview(c, reviewID).Status; got != "EXECUTED" {
		c.Fatalf("review status after execution = %q, want EXECUTED", got)
	}
}

func gwRawFields(c *C, body []byte) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		c.Fatalf("decoding %s: %v", truncate(body), err)
	}
	return m
}

var _ = register(Check{
	ID:    "GW-01",
	Title: "an ad-hoc exec on an agent connection returns its output and records a finished session",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		conn := gwCreateBash(c, "bash", nil)
		// The arithmetic only resolves when bash ran the script.
		res := gwExec(c, conn.Name, "echo parity-gw01-$((20+1))")
		if res.HasReview || !strings.Contains(res.Output, "parity-gw01-21") ||
			res.ExitCode != 0 || res.OutputStatus != "success" {
			c.Fatalf("exec result %+v, want output parity-gw01-21, exit 0, success", res)
		}
		var sess struct {
			Connection string `json:"connection"`
			Verb       string `json:"verb"`
			Status     string `json:"status"`
			ExitCode   *int   `json:"exit_code"`
		}
		err := waitFor(context.Background(), 20*time.Second, func() (bool, error) {
			r := c.Admin().Do(c, http.MethodGet, "/sessions/"+res.SessionID, nil)
			if r.Status != http.StatusOK {
				return false, fmt.Errorf("status %d: %s", r.Status, truncate(r.Body))
			}
			r.JSON(c, &sess)
			return sess.Status == "done", fmt.Errorf("session status %q", sess.Status)
		})
		if err != nil {
			c.Fatalf("session %s never reached done: %v", res.SessionID, err)
		}
		if sess.Connection != conn.Name || sess.Verb != "exec" {
			c.Fatalf("session connection=%q verb=%q, want %q exec", sess.Connection, sess.Verb, conn.Name)
		}
		if sess.ExitCode != nil && *sess.ExitCode != 0 {
			c.Fatalf("session exit_code=%d, want 0", *sess.ExitCode)
		}
	},
})

var _ = register(Check{
	ID:    "GW-02",
	Title: "connection CRUD on an agent connection: create, read, replace, conflict, validation, delete",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		conn := gwCreateBash(c, "crud", nil)
		if conn.AgentID != c.AgentID || conn.Status != "online" || conn.ManagedBy != nil ||
			conn.Type != "custom" || conn.SubType != "custom" {
			c.Fatalf("created connection %+v, want custom/custom online on agent %s, not managed", conn, c.AgentID)
		}

		got := gwGetConnection(c, conn.Name)
		if got.ID != conn.ID || !slices.Equal(got.Command, []string{"/bin/bash"}) || got.AccessModeExec != "enabled" {
			c.Fatalf("read back %+v, want id %s, command [/bin/bash], exec enabled", got, conn.ID)
		}

		body := gwBashBody(c, conn.Name, nil)
		body["connection_tags"] = map[string]string{"team": "parity"}
		body["access_mode_runbooks"] = "disabled"
		c.Admin().Do(c, http.MethodPut, "/connections/"+conn.Name, body).Expect(c, http.StatusOK)
		got = gwGetConnection(c, conn.Name)
		if got.ID != conn.ID || got.ConnectionTags["team"] != "parity" || got.AccessModeRun != "disabled" {
			c.Fatalf("after PUT: %+v, want same id, tag team=parity, runbooks disabled", got)
		}

		c.Admin().Do(c, http.MethodPost, "/connections", gwBashBody(c, conn.Name, nil)).Expect(c, http.StatusConflict)
		c.Admin().Do(c, http.MethodPost, "/connections", gwBashBody(c, "Not A Valid Name!", nil)).
			Expect(c, http.StatusUnprocessableEntity)

		c.Admin().Do(c, http.MethodDelete, "/connections/"+conn.Name, nil).Expect(c, http.StatusNoContent)
		c.Admin().Do(c, http.MethodGet, "/connections/"+conn.Name, nil).Expect(c, http.StatusNotFound)
		c.Admin().Do(c, http.MethodDelete, "/connections/"+conn.Name, nil).Expect(c, http.StatusNotFound)
	},
})

var _ = register(Check{
	ID:    "GW-03",
	Title: "connection list keeps ordinary connections apart from sidecar mirrors, which exist only with the flag on",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		conn := gwCreateBash(c, "plain", nil)

		scName := c.Name("sc")
		var sc struct {
			ID string `json:"id"`
		}
		c.Admin().Do(c, http.MethodPost, "/sidecars", map[string]any{
			"name": scName,
			"configuration": map[string]any{"listeners": []map[string]string{
				{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"},
			}},
		}).Expect(c, http.StatusCreated).JSON(c, &sc)
		if sc.ID == "" {
			c.Fatalf("sidecar create answered no id")
		}

		all := gwListConnections(c, "")
		plain := gwFindConnection(all, conn.Name)
		if plain == nil {
			c.Fatalf("connection %s missing from GET /connections", conn.Name)
		}
		if plain.ManagedBy != nil || plain.AgentID != c.AgentID || plain.ID != conn.ID {
			c.Fatalf("listed connection %+v, want id %s on agent %s, not managed", *plain, conn.ID, c.AgentID)
		}

		mirrors := gwListConnections(c, "?managed_by=sidecar")
		if gwFindConnection(mirrors, conn.Name) != nil {
			c.Fatalf("ordinary connection %s listed under managed_by=sidecar", conn.Name)
		}
		var ours []gwConnection
		for _, m := range mirrors {
			if m.ManagedBy == nil || *m.ManagedBy != "sidecar" {
				c.Fatalf("managed_by=sidecar listed %s with managed_by=%v", m.Name, m.ManagedBy)
			}
			if strings.HasPrefix(m.Name, scName+"-") {
				ours = append(ours, m)
			}
		}
		for _, m := range all {
			if strings.HasPrefix(m.Name, scName+"-") && gwFindConnection(ours, m.Name) == nil {
				c.Fatalf("unfiltered list holds %s but managed_by=sidecar does not", m.Name)
			}
		}
		switch c.Run {
		case GatewayFlagOff:
			if len(ours) != 0 {
				c.Fatalf("flag off, but sidecar %s is mirrored as %+v", scName, ours)
			}
		case GatewayFlagOn:
			if len(ours) != 1 || ours[0].Name != scName+"-appdb" {
				c.Fatalf("flag on, want one mirror %s-appdb, got %+v", scName, ours)
			}
		}

		c.Admin().Do(c, http.MethodDelete, "/sidecars/"+sc.ID, nil).Expect(c, http.StatusNoContent)
		if got := gwGetConnection(c, conn.Name); got.ID != conn.ID || got.ManagedBy != nil {
			c.Fatalf("after the sidecar left, connection %s is %+v", conn.Name, got)
		}
	},
})

var _ = register(Check{
	ID:    "GW-04",
	Title: "a connection with reviewers holds an exec until approved, then runs it once",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		conn := gwCreateBash(c, "review", []string{"admin"})
		if !slices.Equal(conn.Reviewers, []string{"admin"}) {
			c.Fatalf("created connection reviewers=%v, want [admin]", conn.Reviewers)
		}
		const marker = "parity-gw04-2"
		sid := gwExecHeld(c, conn.Name, "echo parity-gw04-$((1+1))", marker)
		rev := gwPendingAdminReview(c, sid)
		if rev.AccessRequestRuleName != nil {
			c.Fatalf("connection review names access request rule %q", *rev.AccessRequestRuleName)
		}

		if got := gwDecide(c, rev.ID, "APPROVED").Status; got != "APPROVED" {
			c.Fatalf("approve answered status %q, want APPROVED", got)
		}
		gwRunApproved(c, sid, rev.ID, marker)
		// One approval runs one execution.
		gwRunReviewed(c, sid).Expect(c, http.StatusForbidden)
	},
})

var _ = register(Check{
	ID:    "GW-05",
	Title: "a rejected review leaves its exec unexecutable and cannot be approved afterwards",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		conn := gwCreateBash(c, "reject", []string{"admin"})
		const marker = "parity-gw05-3"
		sid := gwExecHeld(c, conn.Name, "echo parity-gw05-$((1+2))", marker)
		rev := gwPendingAdminReview(c, sid)

		c.Admin().Do(c, http.MethodPut, "/reviews/"+rev.ID,
			map[string]string{"status": "REJECTED", "rejection_reason": "parity"}).Expect(c, http.StatusOK)
		if got := gwGetReview(c, rev.ID).Status; got != "REJECTED" {
			c.Fatalf("review status after reject = %q, want REJECTED", got)
		}

		r := gwRunReviewed(c, sid).Expect(c, http.StatusForbidden)
		if bytes.Contains(r.Body, []byte(marker)) {
			c.Fatalf("rejected session ran: %s", truncate(r.Body))
		}
		c.Admin().Do(c, http.MethodPut, "/reviews/"+rev.ID, map[string]string{"status": "APPROVED"}).
			Expect(c, http.StatusBadRequest)
		if got := gwGetReview(c, rev.ID).Status; got != "REJECTED" {
			c.Fatalf("review status after a late approve = %q, want REJECTED", got)
		}
	},
})

// gwAccessRule is a command access request rule on connection, reviewed by
// admin, that every requester must pass.
func gwAccessRule(name string, connections []string) map[string]any {
	return map[string]any{
		"name":                     name,
		"access_type":              "command",
		"connection_names":         connections,
		"approval_required_groups": []string{},
		"reviewers_groups":         []string{"admin"},
		"force_approval_groups":    []string{},
		"min_approvals":            1,
	}
}

type gwAccessRuleResp struct {
	Name            string   `json:"name"`
	Description     *string  `json:"description"`
	AccessType      string   `json:"access_type"`
	ConnectionNames []string `json:"connection_names"`
	ReviewersGroups []string `json:"reviewers_groups"`
	MinApprovals    *int     `json:"min_approvals"`
}

var _ = register(Check{
	ID:    "GW-06",
	Title: "a command access request rule holds an agent exec until approved, then runs it",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		conn := gwCreateBash(c, "arr", nil)
		ruleName := c.Name("rule")
		c.Admin().Do(c, http.MethodPost, "/access-requests/rules", gwAccessRule(ruleName, []string{conn.Name})).
			Expect(c, http.StatusCreated)

		const marker = "parity-gw06-12"
		sid := gwExecHeld(c, conn.Name, "echo parity-gw06-$((3*4))", marker)
		rev := gwPendingAdminReview(c, sid)
		if rev.AccessRequestRuleName == nil || *rev.AccessRequestRuleName != ruleName {
			c.Fatalf("review access_request_rule_name=%v, want %s", rev.AccessRequestRuleName, ruleName)
		}

		if got := gwDecide(c, rev.ID, "APPROVED").Status; got != "APPROVED" {
			c.Fatalf("approve answered status %q, want APPROVED", got)
		}
		gwRunApproved(c, sid, rev.ID, marker)
	},
})

var _ = register(Check{
	ID:    "GW-07",
	Title: "access request rule CRUD with the gateway's validation: targets, reviewers, one rule per connection and type",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		ruleName, target := c.Name("rule"), c.Name("conn")
		admin := c.Admin()

		var created gwAccessRuleResp
		admin.Do(c, http.MethodPost, "/access-requests/rules", gwAccessRule(ruleName, []string{target})).
			Expect(c, http.StatusCreated).JSON(c, &created)
		if created.Name != ruleName || created.AccessType != "command" ||
			!slices.Equal(created.ConnectionNames, []string{target}) ||
			!slices.Equal(created.ReviewersGroups, []string{"admin"}) ||
			created.MinApprovals == nil || *created.MinApprovals != 1 {
			c.Fatalf("created rule %+v", created)
		}

		// A gateway rule gates connections: no target is refused.
		noTarget := gwAccessRule(c.Name("empty"), []string{})
		admin.Do(c, http.MethodPost, "/access-requests/rules", noTarget).Expect(c, http.StatusUnprocessableEntity)
		noReviewers := gwAccessRule(c.Name("noreviewers"), []string{c.Name("other")})
		noReviewers["reviewers_groups"] = []string{}
		admin.Do(c, http.MethodPost, "/access-requests/rules", noReviewers).Expect(c, http.StatusUnprocessableEntity)
		badType := gwAccessRule(c.Name("badtype"), []string{c.Name("other")})
		badType["access_type"] = "everything"
		admin.Do(c, http.MethodPost, "/access-requests/rules", badType).Expect(c, http.StatusUnprocessableEntity)
		// One command rule per connection.
		admin.Do(c, http.MethodPost, "/access-requests/rules", gwAccessRule(c.Name("dup"), []string{target})).
			Expect(c, http.StatusUnprocessableEntity)

		var list struct {
			Data []gwAccessRuleResp `json:"data"`
		}
		admin.Do(c, http.MethodGet, "/access-requests/rules", nil).Expect(c, http.StatusOK).JSON(c, &list)
		var names []string
		for _, r := range list.Data {
			names = append(names, r.Name)
		}
		if !slices.Contains(names, ruleName) {
			c.Fatalf("rule %s missing from the list %v", ruleName, names)
		}
		for _, refused := range []string{c.Name("empty"), c.Name("noreviewers"), c.Name("badtype"), c.Name("dup")} {
			if slices.Contains(names, refused) {
				c.Fatalf("refused rule %s was stored", refused)
			}
		}

		update := gwAccessRule(ruleName, []string{target})
		update["description"] = "parity"
		update["reviewers_groups"] = []string{"admin", "sre"}
		admin.Do(c, http.MethodPut, "/access-requests/rules/"+ruleName, update).Expect(c, http.StatusOK)
		var got gwAccessRuleResp
		admin.Do(c, http.MethodGet, "/access-requests/rules/"+ruleName, nil).Expect(c, http.StatusOK).JSON(c, &got)
		if got.Description == nil || *got.Description != "parity" ||
			!slices.Equal(got.ReviewersGroups, []string{"admin", "sre"}) {
			c.Fatalf("after PUT: %+v, want description parity and reviewers [admin sre]", got)
		}

		admin.Do(c, http.MethodDelete, "/access-requests/rules/"+ruleName, nil).Expect(c, http.StatusNoContent)
		admin.Do(c, http.MethodGet, "/access-requests/rules/"+ruleName, nil).Expect(c, http.StatusNotFound)
	},
})

type gwGuardrail struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Input         map[string]any `json:"input"`
	ConnectionIDs []string       `json:"connection_ids"`
}

func gwDenyWords(words ...string) map[string]any {
	return map[string]any{"rules": []map[string]any{
		{"type": "deny_words_list", "words": words, "pattern_regex": ""},
	}}
}

func gwGuardrailBody(name string, connectionIDs []string, words ...string) map[string]any {
	if connectionIDs == nil {
		connectionIDs = []string{}
	}
	return map[string]any{
		"name":           name,
		"description":    "parity",
		"input":          gwDenyWords(words...),
		"output":         map[string]any{"rules": []any{}},
		"connection_ids": connectionIDs,
	}
}

var _ = register(Check{
	ID:    "GW-08",
	Title: "guardrail rule CRUD, with no sidecar fields on an ordinary rule",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		name := c.Name("rule")
		admin := c.Admin()
		r := admin.Do(c, http.MethodPost, "/guardrails", gwGuardrailBody(name, nil, "paritygw08word")).
			Expect(c, http.StatusCreated)
		var created gwGuardrail
		r.JSON(c, &created)
		if created.ID == "" || created.Name != name {
			c.Fatalf("created guardrail %+v", created)
		}
		for _, key := range []string{"sidecar_spec", "sidecar_targets"} {
			if _, ok := gwRawFields(c, r.Body)[key]; ok {
				c.Fatalf("an ordinary guardrail answers %s: %s", key, truncate(r.Body))
			}
		}

		r = admin.Do(c, http.MethodGet, "/guardrails/"+created.ID, nil).Expect(c, http.StatusOK)
		var got gwGuardrail
		r.JSON(c, &got)
		if got.Name != name || got.Description != "parity" || !bytes.Contains(r.Body, []byte("paritygw08word")) {
			c.Fatalf("read back %s", truncate(r.Body))
		}
		for _, key := range []string{"sidecar_spec", "sidecar_targets"} {
			if _, ok := gwRawFields(c, r.Body)[key]; ok {
				c.Fatalf("an ordinary guardrail reads back %s: %s", key, truncate(r.Body))
			}
		}

		admin.Do(c, http.MethodPost, "/guardrails", gwGuardrailBody(name, nil, "x")).Expect(c, http.StatusConflict)

		update := gwGuardrailBody(name, nil, "paritygw08other")
		update["description"] = "parity-updated"
		admin.Do(c, http.MethodPut, "/guardrails/"+created.ID, update).Expect(c, http.StatusOK)
		r = admin.Do(c, http.MethodGet, "/guardrails/"+created.ID, nil).Expect(c, http.StatusOK)
		r.JSON(c, &got)
		if got.Description != "parity-updated" || !bytes.Contains(r.Body, []byte("paritygw08other")) ||
			bytes.Contains(r.Body, []byte("paritygw08word")) {
			c.Fatalf("after PUT: %s", truncate(r.Body))
		}

		var list []gwGuardrail
		admin.Do(c, http.MethodGet, "/guardrails", nil).Expect(c, http.StatusOK).JSON(c, &list)
		if !slices.ContainsFunc(list, func(g gwGuardrail) bool { return g.ID == created.ID }) {
			c.Fatalf("guardrail %s missing from the list", created.ID)
		}

		admin.Do(c, http.MethodDelete, "/guardrails/"+created.ID, nil).Expect(c, http.StatusNoContent)
		admin.Do(c, http.MethodGet, "/guardrails/"+created.ID, nil).Expect(c, http.StatusNotFound)
	},
})

var _ = register(Check{
	ID:    "GW-09",
	Title: "a guardrail bound to an agent connection blocks a matching exec and records the violation",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		conn := gwCreateBash(c, "guarded", nil)
		const word = "paritygw09forbidden"
		var rule gwGuardrail
		c.Admin().Do(c, http.MethodPost, "/guardrails", gwGuardrailBody(c.Name("rule"), []string{conn.ID}, word)).
			Expect(c, http.StatusCreated).JSON(c, &rule)
		if !slices.Contains(rule.ConnectionIDs, conn.ID) {
			c.Fatalf("guardrail connection_ids=%v, want %s", rule.ConnectionIDs, conn.ID)
		}

		ok := gwExec(c, conn.Name, "echo parity-gw09-$((5+5))")
		if !strings.Contains(ok.Output, "parity-gw09-10") || ok.ExitCode != 0 {
			c.Fatalf("an exec the guardrail does not match was refused: %+v", ok)
		}

		blocked := gwExec(c, conn.Name, "echo parity-gw09-$((40+2)); echo "+word)
		if strings.Contains(blocked.Output, "parity-gw09-42") {
			c.Fatalf("the guarded command ran: %+v", blocked)
		}
		if !strings.Contains(blocked.Output, "Guardrails") || blocked.ExitCode == 0 || blocked.HasReview {
			c.Fatalf("blocked exec %+v, want a Guardrails message and a non-zero exit code", blocked)
		}

		var sess struct {
			GuardRailsInfo []struct {
				Direction string `json:"direction"`
				Rule      struct {
					Type  string   `json:"type"`
					Words []string `json:"words"`
				} `json:"rule"`
			} `json:"guardrails_info"`
		}
		err := waitFor(context.Background(), 10*time.Second, func() (bool, error) {
			r := c.Admin().Do(c, http.MethodGet, "/sessions/"+blocked.SessionID, nil)
			if r.Status != http.StatusOK {
				return false, fmt.Errorf("status %d: %s", r.Status, truncate(r.Body))
			}
			r.JSON(c, &sess)
			return len(sess.GuardRailsInfo) > 0, fmt.Errorf("no guardrails_info: %s", truncate(r.Body))
		})
		if err != nil {
			c.Fatalf("blocked session %s: %v", blocked.SessionID, err)
		}
		info := sess.GuardRailsInfo[0]
		if info.Direction != "input" || info.Rule.Type != "deny_words_list" || !slices.Contains(info.Rule.Words, word) {
			c.Fatalf("session guardrails_info %+v, want an input deny_words_list hit on %s", sess.GuardRailsInfo, word)
		}
	},
})

var _ = register(Check{
	ID:    "GW-10",
	Title: "a data masking rule is refused with 422 when no DLP provider is configured, and nothing is stored",
	Runs:  gatewayRuns,
	Fn: func(c *C) {
		name := c.Name("mask")
		c.Admin().Do(c, http.MethodPost, "/datamasking-rules", map[string]any{
			"name":           name,
			"description":    "parity",
			"connection_ids": []string{},
			"supported_entity_types": []map[string]any{
				{"name": "PII", "entity_types": []string{"EMAIL_ADDRESS"}},
			},
		}).Expect(c, http.StatusUnprocessableEntity)

		var list []struct {
			Name string `json:"name"`
		}
		c.Admin().Do(c, http.MethodGet, "/datamasking-rules", nil).Expect(c, http.StatusOK).JSON(c, &list)
		for _, r := range list {
			if r.Name == name {
				c.Fatalf("the refused data masking rule %s was stored", name)
			}
		}
	},
})

// Today a gateway refuses sidecar_spec with 422 (sidecarbind.Refuse). With
// the control-plane mode gone the gateway serves the control plane's rule
// writes, so the flag-off answer is expected to change too: only the flag-on
// target is asserted.
var _ = register(Check{
	ID:    "GW-11",
	Title: "a guardrail carrying a sidecar_spec is stored and read back on the gateway",
	Runs:  map[Run]Expect{GatewayFlagOn: PendingOn("ENG-524")},
	Fn: func(c *C) {
		spec := json.RawMessage(`{"rules":[{"name":"r","type":"operation","operations":["drop","truncate"]}]}`)
		body := gwGuardrailBody(c.Name("rule"), nil, "paritygw11word")
		body["sidecar_spec"] = spec
		var created gwGuardrail
		c.Admin().Do(c, http.MethodPost, "/guardrails", body).Expect(c, http.StatusCreated).JSON(c, &created)

		r := c.Admin().Do(c, http.MethodGet, "/guardrails/"+created.ID, nil).Expect(c, http.StatusOK)
		var want, got any
		if err := json.Unmarshal(spec, &want); err != nil {
			c.Fatalf("decoding the sent spec: %v", err)
		}
		raw, ok := gwRawFields(c, r.Body)["sidecar_spec"]
		if !ok {
			c.Fatalf("read back no sidecar_spec: %s", truncate(r.Body))
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			c.Fatalf("decoding sidecar_spec %s: %v", raw, err)
		}
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)
		if !bytes.Equal(wantJSON, gotJSON) {
			c.Fatalf("sidecar_spec read back %s, want %s", gotJSON, wantJSON)
		}
	},
})
