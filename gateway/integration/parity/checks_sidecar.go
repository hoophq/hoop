//go:build integration && parity

package parity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// The sidecar surface as a sidecar and its admin reach it: the admin routes
// under /sidecars and the token-authenticated daemon routes. Both modes serve
// the same routes today, so most of these are Must on every live run.

const (
	scpVersion         = "1.191.0"
	scpSessionEvents   = "experimental.sidecar_session_events"
	scpLicenseManaged  = "hoop-sidecar-license-managed"
	scpConfigRevision  = "hoop-sidecar-config-revision"
	scpSessionEventsHd = "hoop-sidecar-session-events"
	// scpForbiddenWord is the word of the guardrail rule an imported file
	// carries, so its presence in a document is easy to find.
	scpForbiddenWord = "parity-forbidden-word"
)

var scpRevisionRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// scpOneListener is the smallest configuration a sidecar can serve.
const scpOneListener = `{"listeners": [{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"}]}`

// scpImportFile is a daemon config file with rules embedded in its listener,
// and the license a standalone sidecar names in its own file.
const scpImportFile = `{"license": "standalone-license", "listeners": [{
	"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432",
	"guardrails": {"rules": [{"name": "own", "type": "deny_words_list", "words": ["` + scpForbiddenWord + `"]}]},
	"mask": {"rules": [{"name": "emails", "entities": ["EMAIL_ADDRESS"], "strategy": "redact"}]}
}]}`

type scpSidecar struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Token           *string         `json:"token"`
	Configuration   json.RawMessage `json:"configuration"`
	Version         string          `json:"version"`
	LastSeenAt      *time.Time      `json:"last_seen_at"`
	ServedRevision  string          `json:"served_revision"`
	BoundRules      []scpBinding    `json:"bound_rules"`
	BoundRulesError bool            `json:"bound_rules_unavailable"`
	DetachedRules   *struct {
		Deleted struct {
			Guardrails  []string `json:"guardrails"`
			DataMasking []string `json:"data_masking"`
		} `json:"deleted"`
	} `json:"detached_rules"`
}

type scpBinding struct {
	Kind         string `json:"kind"`
	RuleName     string `json:"rule_name"`
	ListenerName string `json:"listener_name"`
}

// scpServed is the part of a served document the checks read.
type scpServed struct {
	Listeners []struct {
		Name     string `json:"name"`
		Protocol string `json:"protocol"`
		Upstream string `json:"upstream"`
	} `json:"listeners"`
	License      *string `json:"license"`
	LoadFromDisk *bool   `json:"load_from_disk"`
	LogLevel     string  `json:"log_level"`
}

type scpConnection struct {
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	SubType            string  `json:"subtype"`
	Status             string  `json:"status"`
	ManagedBy          *string `json:"managed_by"`
	AccessModeConnect  string  `json:"access_mode_connect"`
	AccessModeExec     string  `json:"access_mode_exec"`
	AccessModeRunbooks string  `json:"access_mode_runbooks"`
}

// scpCreate registers a sidecar; config is a raw JSON document or "" for
// none.
func scpCreate(c *C, name, config string) scpSidecar {
	body := map[string]any{"name": name}
	if config != "" {
		body["configuration"] = json.RawMessage(config)
	}
	var sc scpSidecar
	c.Admin().Do(c, http.MethodPost, "/sidecars", body).Expect(c, http.StatusCreated).JSON(c, &sc)
	if sc.Token == nil || *sc.Token == "" {
		c.Fatalf("POST /sidecars answered 201 with no token: %+v", sc)
	}
	return sc
}

func scpGet(c *C, nameOrID string) scpSidecar {
	var sc scpSidecar
	c.Admin().Do(c, http.MethodGet, "/sidecars/"+nameOrID, nil).Expect(c, http.StatusOK).JSON(c, &sc)
	return sc
}

func scpHandshake(c *C, token string) Resp {
	return c.Sidecar(token).Do(c, http.MethodPost, "/sidecars/handshake", map[string]string{"version": scpVersion})
}

// scpServedOK handshakes and checks what every served answer carries.
func scpServedOK(c *C, token string) (scpServed, string, Resp) {
	r := scpHandshake(c, token).Expect(c, http.StatusOK)
	if got := r.Header.Get(scpLicenseManaged); got != "true" {
		c.Fatalf("handshake %s=%q, want true", scpLicenseManaged, got)
	}
	rev := r.Header.Get(scpConfigRevision)
	if !scpRevisionRe.MatchString(rev) {
		c.Fatalf("handshake %s=%q, want 32 lowercase hex characters", scpConfigRevision, rev)
	}
	var doc scpServed
	r.JSON(c, &doc)
	if doc.License == nil || *doc.License == "" {
		c.Fatalf("handshake served no license: %s", truncate(r.Body))
	}
	if doc.LoadFromDisk != nil {
		c.Fatalf("a plane-owned document must not carry load_from_disk: %s", truncate(r.Body))
	}
	return doc, rev, r
}

func scpPut(c *C, name, config string) Resp {
	return c.Admin().Do(c, http.MethodPut, "/sidecars/"+name, map[string]any{"configuration": json.RawMessage(config)})
}

func scpPatch(c *C, name, config string) Resp {
	return c.Admin().Do(c, http.MethodPatch, "/sidecars/"+name, map[string]any{"configuration": json.RawMessage(config)})
}

func scpDelete(c *C, name string) {
	c.Admin().Do(c, http.MethodDelete, "/sidecars/"+name, nil).Expect(c, http.StatusNoContent)
}

// scpMirrors lists the connections the API reports as sidecar-managed, by
// name.
func scpMirrors(c *C) map[string]scpConnection {
	var conns []scpConnection
	c.Admin().Do(c, http.MethodGet, "/connections?managed_by=sidecar", nil).Expect(c, http.StatusOK).JSON(c, &conns)
	out := map[string]scpConnection{}
	for _, conn := range conns {
		out[conn.Name] = conn
	}
	return out
}

// scpFlag reads a feature flag through /serverinfo, as the UI does.
func scpFlag(c *C, name string) bool {
	var info struct {
		FeatureFlags map[string]bool `json:"feature_flags"`
	}
	c.Admin().Do(c, http.MethodGet, "/serverinfo", nil).Expect(c, http.StatusOK).JSON(c, &info)
	return info.FeatureFlags[name]
}

func scpSetFlag(c *C, name string, on bool) {
	c.Admin().Do(c, http.MethodPut, "/feature-flags/"+name, map[string]any{"enabled": on}).Expect(c, http.StatusOK)
}

var _ = register(Check{
	ID:    "SC-01",
	Title: "an admin creates a sidecar, gets its token once, and lists and reads it by name and id",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("edge")
		created := scpCreate(c, name, scpOneListener)
		if !strings.HasPrefix(*created.Token, "hsc_") {
			c.Fatalf("token %q does not have the hsc_ prefix sidecars are configured with", *created.Token)
		}
		if created.ID == "" || created.Name != name {
			c.Fatalf("create answered id=%q name=%q, want an id and name %q", created.ID, created.Name, name)
		}

		for _, key := range []string{name, created.ID} {
			got := scpGet(c, key)
			if got.ID != created.ID || got.Name != name {
				c.Fatalf("GET /sidecars/%s = id %q name %q, want %q %q", key, got.ID, got.Name, created.ID, name)
			}
			if got.Token != nil {
				c.Fatalf("GET /sidecars/%s returned the token again", key)
			}
		}
		r := c.Admin().Do(c, http.MethodGet, "/sidecars", nil).Expect(c, http.StatusOK)
		var list []scpSidecar
		r.JSON(c, &list)
		found := false
		for _, s := range list {
			if s.Token != nil {
				c.Fatalf("GET /sidecars returned a token for %s", s.Name)
			}
			found = found || s.ID == created.ID
		}
		if !found {
			c.Fatalf("GET /sidecars does not list %s: %s", name, truncate(r.Body))
		}

		c.Admin().Do(c, http.MethodPost, "/sidecars",
			map[string]any{"name": name, "configuration": json.RawMessage(scpOneListener)}).Expect(c, http.StatusConflict)
		c.Admin().Do(c, http.MethodPost, "/sidecars", map[string]any{"name": "handshake"}).Expect(c, http.StatusUnprocessableEntity)
		c.Admin().Do(c, http.MethodGet, "/sidecars/"+c.Name("missing"), nil).Expect(c, http.StatusNotFound)
	},
})

var _ = register(Check{
	ID:    "SC-02",
	Title: "the handshake serves the configuration with its revision, the license-managed header and the license, and records the check-in",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("edge")
		sc := scpCreate(c, name, scpOneListener)
		if got := scpGet(c, name); got.LastSeenAt != nil || got.Version != "" {
			c.Fatalf("a sidecar that never handshook reads last_seen_at=%v version=%q", got.LastSeenAt, got.Version)
		}

		doc, rev, _ := scpServedOK(c, *sc.Token)
		if len(doc.Listeners) != 1 || doc.Listeners[0].Name != "appdb" || doc.Listeners[0].Protocol != "postgres" ||
			doc.Listeners[0].Upstream != "db:5432" {
			c.Fatalf("served listeners %+v, want the appdb postgres listener", doc.Listeners)
		}
		_, again, _ := scpServedOK(c, *sc.Token)
		if again != rev {
			c.Fatalf("the revision changed with no write: %s then %s", rev, again)
		}

		got := scpGet(c, name)
		if got.Version != scpVersion || got.LastSeenAt == nil || got.ServedRevision != rev {
			c.Fatalf("after the handshake version=%q last_seen_at=%v served_revision=%q, want %q, set, %q",
				got.Version, got.LastSeenAt, got.ServedRevision, scpVersion, rev)
		}
		c.Sidecar(*sc.Token).Do(c, http.MethodPost, "/sidecars/handshake", map[string]string{}).Expect(c, http.StatusBadRequest)
	},
})

var _ = register(Check{
	ID:    "SC-03",
	Title: "a sidecar with no listeners assigned gets 412 on the handshake and is not recorded as seen",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("empty")
		sc := scpCreate(c, name, "")
		scpHandshake(c, *sc.Token).Expect(c, http.StatusPreconditionFailed)
		if got := scpGet(c, name); got.LastSeenAt != nil || got.Version != "" {
			c.Fatalf("a refused handshake was recorded: last_seen_at=%v version=%q", got.LastSeenAt, got.Version)
		}
	},
})

var _ = register(Check{
	ID:    "SC-04",
	Title: "GET /sidecars/configuration serves the handshake's document and records nothing",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("edge")
		sc := scpCreate(c, name, scpOneListener)
		r := c.Sidecar(*sc.Token).Do(c, http.MethodGet, "/sidecars/configuration", nil).Expect(c, http.StatusOK)
		if got := r.Header.Get(scpLicenseManaged); got != "true" {
			c.Fatalf("configuration %s=%q, want true", scpLicenseManaged, got)
		}
		if got := scpGet(c, name); got.LastSeenAt != nil || got.Version != "" {
			c.Fatalf("a configuration poll was recorded as a check-in: last_seen_at=%v version=%q", got.LastSeenAt, got.Version)
		}
		_, _, hs := scpServedOK(c, *sc.Token)
		var polled, served map[string]any
		r.JSON(c, &polled)
		hs.JSON(c, &served)
		pj, _ := json.Marshal(polled)
		sj, _ := json.Marshal(served)
		if !bytes.Equal(pj, sj) {
			c.Fatalf("GET /sidecars/configuration and the handshake serve different documents:\n%s\n%s", truncate(pj), truncate(sj))
		}
	},
})

var _ = register(Check{
	ID:    "SC-05",
	Title: "PUT and PATCH of a sidecar configuration change the served revision; PATCH merges into the stored document",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("edge")
		sc := scpCreate(c, name, scpOneListener)
		_, rev0, _ := scpServedOK(c, *sc.Token)

		scpPut(c, name, `{"listeners": [{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db2:5432"}]}`).
			Expect(c, http.StatusOK)
		doc, rev1, _ := scpServedOK(c, *sc.Token)
		if rev1 == rev0 || len(doc.Listeners) != 1 || doc.Listeners[0].Upstream != "db2:5432" {
			c.Fatalf("after PUT revision %s -> %s, listeners %+v; want a new revision serving db2:5432", rev0, rev1, doc.Listeners)
		}

		scpPatch(c, name, `{"log_level": "debug"}`).Expect(c, http.StatusOK)
		doc, rev2, _ := scpServedOK(c, *sc.Token)
		if rev2 == rev1 || doc.LogLevel != "debug" {
			c.Fatalf("after PATCH revision %s -> %s, log_level %q; want a new revision with log_level debug", rev1, rev2, doc.LogLevel)
		}
		if len(doc.Listeners) != 1 || doc.Listeners[0].Upstream != "db2:5432" {
			c.Fatalf("PATCH of log_level lost the stored listeners: %+v", doc.Listeners)
		}

		// The owner switch only goes through PATCH.
		scpPut(c, name, `{"load_from_disk": true, "listeners": [{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db2:5432"}]}`).
			Expect(c, http.StatusUnprocessableEntity)
		scpPut(c, name, `{"listeners": "nope"}`).Expect(c, http.StatusUnprocessableEntity)
		scpPut(c, c.Name("missing"), scpOneListener).Expect(c, http.StatusNotFound)
		if _, rev3, _ := scpServedOK(c, *sc.Token); rev3 != rev2 {
			c.Fatalf("a refused PUT changed the revision: %s -> %s", rev2, rev3)
		}
	},
})

var _ = register(Check{
	ID:    "SC-06",
	Title: "PATCH load_from_disk hands the document to the sidecar's file and back",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("edge")
		sc := scpCreate(c, name, scpOneListener)

		scpPatch(c, name, `{"load_from_disk": true}`).Expect(c, http.StatusOK)
		for _, r := range []Resp{
			scpHandshake(c, *sc.Token).Expect(c, http.StatusOK),
			c.Sidecar(*sc.Token).Do(c, http.MethodGet, "/sidecars/configuration", nil).Expect(c, http.StatusOK),
		} {
			var doc map[string]json.RawMessage
			r.JSON(c, &doc)
			if string(doc["load_from_disk"]) != "true" || len(doc["license"]) == 0 || len(doc) != 2 {
				c.Fatalf("a file-owned sidecar must receive only load_from_disk true and the license, got %s", truncate(r.Body))
			}
			if r.Header.Get(scpLicenseManaged) != "true" {
				c.Fatalf("a file-owned answer lost %s", scpLicenseManaged)
			}
			if rev := r.Header.Get(scpConfigRevision); rev != "" {
				c.Fatalf("a file-owned answer carries a revision %q", rev)
			}
		}
		if got := scpGet(c, name); got.LastSeenAt == nil {
			c.Fatalf("a file-owned sidecar that handshook is not recorded as seen")
		}

		scpPatch(c, name, `{"load_from_disk": false, "log_level": "debug"}`).Expect(c, http.StatusUnprocessableEntity)
		var back scpSidecar
		scpPatch(c, name, `{"load_from_disk": false}`).Expect(c, http.StatusOK).JSON(c, &back)
		var stored scpServed
		if err := json.Unmarshal(back.Configuration, &stored); err != nil {
			c.Fatalf("decoding the stored configuration %s: %v", truncate(back.Configuration), err)
		}
		if len(stored.Listeners) != 0 || (stored.LoadFromDisk != nil && *stored.LoadFromDisk) {
			c.Fatalf("the switch back must empty the document so the file is imported again, got %s", truncate(back.Configuration))
		}
		scpHandshake(c, *sc.Token).Expect(c, http.StatusPreconditionFailed)
	},
})

var _ = register(Check{
	ID:    "SC-07",
	Title: "a sidecar imports its config file once; the plane adopts it, splits the embedded rules into rule items and serves them back",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("imp")
		sc := scpCreate(c, name, "")
		scpHandshake(c, *sc.Token).Expect(c, http.StatusPreconditionFailed)

		r := c.Sidecar(*sc.Token).Do(c, http.MethodPut, "/sidecars/configuration", []byte(scpImportFile)).Expect(c, http.StatusOK)
		if bytes.Contains(r.Body, []byte(scpForbiddenWord)) || bytes.Contains(r.Body, []byte("standalone-license")) {
			c.Fatalf("the adopted document keeps the embedded rules or the file's license: %s", truncate(r.Body))
		}
		var adopted scpServed
		r.JSON(c, &adopted)
		if len(adopted.Listeners) != 1 || adopted.Listeners[0].Name != "appdb" {
			c.Fatalf("the adopted document lost the listener: %s", truncate(r.Body))
		}

		guardrail, masking := name+"-appdb-own", name+"-appdb-emails"
		got := scpGet(c, name)
		if got.BoundRulesError {
			c.Fatalf("the sidecar's bindings could not be read")
		}
		want := map[string]string{guardrail: "guardrail", masking: "datamasking"}
		for _, b := range got.BoundRules {
			if kind, ok := want[b.RuleName]; ok && kind == b.Kind && b.ListenerName == "appdb" {
				delete(want, b.RuleName)
			}
		}
		if len(want) != 0 {
			c.Fatalf("bound_rules %+v miss %v", got.BoundRules, want)
		}
		var rules []struct {
			Name string `json:"name"`
		}
		gr := c.Admin().Do(c, http.MethodGet, "/guardrails", nil).Expect(c, http.StatusOK)
		gr.JSON(c, &rules)
		listed := false
		for _, rule := range rules {
			listed = listed || rule.Name == guardrail
		}
		if !listed {
			c.Fatalf("GET /guardrails does not list the imported rule %s: %s", guardrail, truncate(gr.Body))
		}

		_, _, hs := scpServedOK(c, *sc.Token)
		if !bytes.Contains(hs.Body, []byte(scpForbiddenWord)) {
			c.Fatalf("the served document does not enforce the imported guardrail: %s", truncate(hs.Body))
		}

		c.Sidecar(*sc.Token).Do(c, http.MethodPut, "/sidecars/configuration", []byte(scpImportFile)).Expect(c, http.StatusConflict)
		c.Sidecar(*sc.Token).Do(c, http.MethodPut, "/sidecars/configuration", []byte(`{"listeners": []}`)).
			Expect(c, http.StatusUnprocessableEntity)
	},
})

var _ = register(Check{
	ID:    "SC-08",
	Title: "every sidecar route refuses a missing or unknown token with 401",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		routes := []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, "/sidecars/handshake", map[string]string{"version": scpVersion}},
			{http.MethodGet, "/sidecars/configuration", nil},
			{http.MethodPut, "/sidecars/configuration", []byte(scpOneListener)},
			{http.MethodPost, "/sidecars/events", map[string]any{"events": []any{}}},
			{http.MethodGet, "/sidecars/reviews", nil},
		}
		for _, cl := range []*Client{c.Anonymous(), c.Sidecar("hsc_not-a-token"), c.Sidecar(c.Token)} {
			for _, rt := range routes {
				r := cl.Do(c, rt.method, rt.path, rt.body)
				if r.Status != http.StatusUnauthorized {
					c.Fatalf("%s %s with header %v: status %d, want 401: %s", rt.method, rt.path, cl.header, r.Status, truncate(r.Body))
				}
			}
		}
		// A sidecar token is not a user credential.
		sc := scpCreate(c, c.Name("edge"), scpOneListener)
		(&Client{base: c.API, header: http.Header{"Authorization": {"Bearer " + *sc.Token}}}).
			Do(c, http.MethodGet, "/sidecars", nil).Expect(c, http.StatusUnauthorized)
	},
})

var _ = register(Check{
	ID:    "SC-09",
	Title: "deleting a sidecar revokes its token at once",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("edge")
		sc := scpCreate(c, name, scpOneListener)
		scpServedOK(c, *sc.Token)
		scpDelete(c, name)
		scpHandshake(c, *sc.Token).Expect(c, http.StatusUnauthorized)
		c.Sidecar(*sc.Token).Do(c, http.MethodGet, "/sidecars/configuration", nil).Expect(c, http.StatusUnauthorized)
		c.Admin().Do(c, http.MethodGet, "/sidecars/"+name, nil).Expect(c, http.StatusNotFound)
		c.Admin().Do(c, http.MethodDelete, "/sidecars/"+name, nil).Expect(c, http.StatusNotFound)
	},
})

// scpEvents is one sidecar session on listener appdb, start to end.
const scpEvents = `{"events": [
	{"seq": 1, "event": {"kind": "session_start", "timestamp": "2026-01-02T03:04:05Z", "session_id": "s1",
		"principal": "alice@example.com", "protocol": "postgres", "connection": "appdb", "allowed": false}},
	{"seq": 2, "event": {"kind": "statement", "timestamp": "2026-01-02T03:04:06Z", "session_id": "s1",
		"principal": "alice@example.com", "protocol": "postgres", "connection": "appdb", "statement": "SELECT 1", "allowed": true}},
	{"seq": 3, "event": {"kind": "session_end", "timestamp": "2026-01-02T03:04:07Z", "session_id": "s1",
		"principal": "alice@example.com", "protocol": "postgres", "connection": "appdb", "allowed": false,
		"statement_count": 1, "duration_ns": 2000000000}}
]}`

var _ = register(Check{
	ID:    "SC-10",
	Title: "with experimental.sidecar_session_events on, the handshake offers session events and POST /sidecars/events records a session idempotently",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("edge")
		sc := scpCreate(c, name, scpOneListener)
		sidecar := c.Sidecar(*sc.Token)

		was := scpFlag(c, scpSessionEvents)
		defer func() {
			if _, err := c.Admin().do(http.MethodPut, "/feature-flags/"+scpSessionEvents, map[string]any{"enabled": was}); err != nil {
				c.Logf("restoring %s: %v", scpSessionEvents, err)
			}
		}()

		scpSetFlag(c, scpSessionEvents, false)
		if _, _, r := scpServedOK(c, *sc.Token); r.Header.Get(scpSessionEventsHd) != "" {
			c.Fatalf("flag off: the handshake offers session events (%s=%q)", scpSessionEventsHd, r.Header.Get(scpSessionEventsHd))
		}
		sidecar.Do(c, http.MethodPost, "/sidecars/events", []byte(scpEvents)).Expect(c, http.StatusPreconditionFailed)

		scpSetFlag(c, scpSessionEvents, true)
		if _, _, r := scpServedOK(c, *sc.Token); r.Header.Get(scpSessionEventsHd) != "true" {
			c.Fatalf("flag on: handshake %s=%q, want true", scpSessionEventsHd, r.Header.Get(scpSessionEventsHd))
		}
		type counts struct {
			Accepted   int `json:"accepted"`
			Duplicates int `json:"duplicates"`
		}
		var first, resend counts
		sidecar.Do(c, http.MethodPost, "/sidecars/events", []byte(scpEvents)).Expect(c, http.StatusOK).JSON(c, &first)
		if first != (counts{Accepted: 3}) {
			c.Fatalf("first batch answered %+v, want 3 accepted and no duplicate", first)
		}
		sidecar.Do(c, http.MethodPost, "/sidecars/events", []byte(scpEvents)).Expect(c, http.StatusOK).JSON(c, &resend)
		if resend != (counts{Duplicates: 3}) {
			c.Fatalf("resent batch answered %+v, want 3 duplicates and none accepted", resend)
		}
		sidecar.Do(c, http.MethodPost, "/sidecars/events", []byte(`{"events": [{"seq": 0, "event": {"session_id": "s2"}}]}`)).
			Expect(c, http.StatusBadRequest)

		conn := name + "-appdb"
		var list struct {
			Data []struct {
				ID         string `json:"id"`
				Connection string `json:"connection"`
				Status     string `json:"status"`
			} `json:"data"`
		}
		lr := c.Admin().Do(c, http.MethodGet, "/sessions?connection="+url.QueryEscape(conn), nil).Expect(c, http.StatusOK)
		lr.JSON(c, &list)
		if len(list.Data) != 1 || list.Data[0].Connection != conn || list.Data[0].Status != "done" {
			c.Fatalf("GET /sessions?connection=%s = %s, want the one done sidecar session", conn, truncate(lr.Body))
		}
		c.Admin().Do(c, http.MethodGet, "/sessions/"+list.Data[0].ID, nil).Expect(c, http.StatusOK)

		scpSetFlag(c, scpSessionEvents, false)
		if _, _, r := scpServedOK(c, *sc.Token); r.Header.Get(scpSessionEventsHd) != "" {
			c.Fatalf("flag turned off: the handshake still offers session events")
		}
		sidecar.Do(c, http.MethodPost, "/sidecars/events", []byte(scpEvents)).Expect(c, http.StatusPreconditionFailed)
	},
})

var _ = register(Check{
	ID:    "SC-11",
	Title: "with beta.sidecar_listeners on, each listener is a <sidecar>-<listener> connection with every access mode disabled, owned by the sidecar",
	Runs:  map[Run]Expect{GatewayFlagOn: Must},
	Fn: func(c *C) {
		name := c.Name("pay")
		scpCreate(c, name, `{"listeners": [
			{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"},
			{"name": "api", "protocol": "http", "listen": ":8080", "upstream": "api:80"}]}`)
		appdb, api := name+"-appdb", name+"-api"
		mirrors := scpMirrors(c)
		for conn, kind := range map[string][2]string{appdb: {"database", "postgres"}, api: {"httpproxy", "httpproxy"}} {
			m, ok := mirrors[conn]
			if !ok {
				c.Fatalf("GET /connections?managed_by=sidecar does not list %s: %v", conn, mirrors)
			}
			if m.Type != kind[0] || m.SubType != kind[1] || m.ManagedBy == nil || *m.ManagedBy != "sidecar" {
				c.Fatalf("%s is %s/%s managed_by=%v, want %s/%s managed_by sidecar", conn, m.Type, m.SubType, m.ManagedBy, kind[0], kind[1])
			}
			if m.AccessModeConnect != "disabled" || m.AccessModeExec != "disabled" || m.AccessModeRunbooks != "disabled" {
				c.Fatalf("%s access modes connect=%q exec=%q runbooks=%q, want all disabled",
					conn, m.AccessModeConnect, m.AccessModeExec, m.AccessModeRunbooks)
			}
		}

		c.Admin().Do(c, http.MethodDelete, "/connections/"+appdb, nil).Expect(c, http.StatusConflict)
		if _, ok := scpMirrors(c)[appdb]; !ok {
			c.Fatalf("a refused DELETE /connections/%s removed the mirror", appdb)
		}

		scpPut(c, name, `{"listeners": [{"name": "appdb", "protocol": "mysql", "listen": ":3306", "upstream": "db:3306"}]}`).
			Expect(c, http.StatusOK)
		mirrors = scpMirrors(c)
		if m, ok := mirrors[appdb]; !ok || m.SubType != "mysql" {
			c.Fatalf("after PUT %s = %+v (listed %v), want the mysql mirror", appdb, m, ok)
		}
		if _, ok := mirrors[api]; ok {
			c.Fatalf("after PUT dropped listener api, its mirror %s is still listed", api)
		}

		scpDelete(c, name)
		if _, ok := scpMirrors(c)[appdb]; ok {
			c.Fatalf("deleting the sidecar left its mirror %s", appdb)
		}
	},
})

var _ = register(Check{
	ID:    "SC-12",
	Title: "with beta.sidecar_listeners off, sidecar writes and imports create no connection",
	Runs:  map[Run]Expect{GatewayFlagOff: Must, ControlPlane: Must},
	Fn: func(c *C) {
		before := scpMirrors(c)
		name := c.Name("pay")
		scpCreate(c, name, `{"listeners": [
			{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"},
			{"name": "app db", "protocol": "postgres", "listen": ":5433", "upstream": "db:5432"}]}`)
		imp := scpCreate(c, c.Name("imp"), "")
		c.Sidecar(*imp.Token).Do(c, http.MethodPut, "/sidecars/configuration", []byte(scpImportFile)).Expect(c, http.StatusOK)
		after := scpMirrors(c)
		if len(after) != len(before) {
			c.Fatalf("sidecar-managed connections went from %d to %d with the flag off: %v", len(before), len(after), after)
		}
		for _, conn := range []string{name + "-appdb", c.Name("imp") + "-appdb"} {
			c.Admin().Do(c, http.MethodGet, "/connections/"+conn, nil).Expect(c, http.StatusNotFound)
		}
	},
})

var _ = register(Check{
	ID:    "SC-13",
	Title: "a sidecar's mirror connections read online once it checks in",
	Runs:  map[Run]Expect{GatewayFlagOn: PendingOn("ENG-530")},
	Fn: func(c *C) {
		name := c.Name("pay")
		sc := scpCreate(c, name, scpOneListener)
		conn := name + "-appdb"
		if m, ok := scpMirrors(c)[conn]; !ok || m.Status != "offline" {
			c.Fatalf("before any check-in %s = %+v (listed %v), want offline", conn, m, ok)
		}
		scpServedOK(c, *sc.Token)
		var status string
		err := waitFor(context.Background(), 10*time.Second, func() (bool, error) {
			r, err := c.Admin().do(http.MethodGet, "/connections/"+conn, nil)
			if err != nil {
				return false, err
			}
			var m scpConnection
			if err := json.Unmarshal(r.Body, &m); err != nil {
				return false, fmt.Errorf("status %d: %s", r.Status, truncate(r.Body))
			}
			status = m.Status
			return status == "online", nil
		})
		if err != nil {
			c.Fatalf("after the check-in %s status=%q, want online: %v", conn, status, err)
		}
	},
})

// scpLicense reads the organization's license document as stored.
func scpLicense(c *C) sql.NullString {
	var doc sql.NullString
	if err := c.DB.QueryRow(`SELECT license_data::text FROM private.orgs WHERE id = $1`, c.OrgID).Scan(&doc); err != nil {
		c.Fatalf("reading the org license: %v", err)
	}
	if !doc.Valid || doc.String == "" {
		c.Fatalf("the run's organization holds no license")
	}
	return doc
}

var _ = register(Check{
	ID:    "LIC-01",
	Title: "sidecar admin and daemon routes answer 403 while the organization holds no Enterprise license, and 2xx again once it does",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		sc := scpCreate(c, c.Name("edge"), scpOneListener)
		imp := scpCreate(c, c.Name("imp"), "")
		original := scpLicense(c)

		type call struct {
			cl           *Client
			method, path string
			body         any
			ok           int
		}
		calls := []call{
			{c.Admin(), http.MethodGet, "/sidecars", nil, http.StatusOK},
			{c.Admin(), http.MethodGet, "/sidecars/" + sc.Name, nil, http.StatusOK},
			{c.Admin(), http.MethodPut, "/sidecars/" + sc.Name, map[string]any{"configuration": json.RawMessage(scpOneListener)}, http.StatusOK},
			{c.Admin(), http.MethodPatch, "/sidecars/" + sc.Name, map[string]any{"configuration": json.RawMessage(`{"log_level": "info"}`)}, http.StatusOK},
			{c.Sidecar(*sc.Token), http.MethodPost, "/sidecars/handshake", map[string]string{"version": scpVersion}, http.StatusOK},
			{c.Sidecar(*sc.Token), http.MethodGet, "/sidecars/configuration", nil, http.StatusOK},
		}
		unlicensed := append(append([]call{}, calls...),
			call{c.Admin(), http.MethodPost, "/sidecars", map[string]any{"name": c.Name("unlicensed")}, 0},
			call{c.Admin(), http.MethodDelete, "/sidecars/" + sc.Name, nil, 0},
			call{c.Sidecar(*imp.Token), http.MethodPut, "/sidecars/configuration", []byte(scpImportFile), 0},
			call{c.Sidecar(*sc.Token), http.MethodPost, "/sidecars/events", []byte(scpEvents), 0},
		)

		restored := false
		restore := func() {
			if restored {
				return
			}
			restored = true
			if _, err := c.DB.Exec(`UPDATE private.orgs SET license_data = $1::json WHERE id = $2`, original.String, c.OrgID); err != nil {
				c.Fatalf("restoring the org license: %v", err)
			}
		}
		defer restore()
		if _, err := c.DB.Exec(`UPDATE private.orgs SET license_data = NULL WHERE id = $1`, c.OrgID); err != nil {
			c.Fatalf("removing the org license: %v", err)
		}
		for _, k := range unlicensed {
			r := k.cl.Do(c, k.method, k.path, k.body)
			if r.Status != http.StatusForbidden || !bytes.Contains(bytes.ToLower(r.Body), []byte("enterprise license")) {
				c.Fatalf("no license: %s %s = %d %s, want 403 naming the Enterprise license", k.method, k.path, r.Status, truncate(r.Body))
			}
		}
		// The reviews route is not license-gated: a lapsed license must not
		// strand statements a sidecar holds.
		if r := c.Sidecar(*sc.Token).Do(c, http.MethodGet, "/sidecars/reviews", nil); r.Status == http.StatusForbidden {
			c.Fatalf("no license: GET /sidecars/reviews answered 403: %s", truncate(r.Body))
		}

		restore()
		for _, k := range calls {
			k.cl.Do(c, k.method, k.path, k.body).Expect(c, k.ok)
		}
		c.Sidecar(*imp.Token).Do(c, http.MethodPut, "/sidecars/configuration", []byte(scpImportFile)).Expect(c, http.StatusOK)
	},
})

var _ = register(Check{
	ID:    "LIC-02",
	Title: "the served sidecar configuration carries the organization's license, and a per-sidecar license is refused",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		name := c.Name("edge")
		sc := scpCreate(c, name, scpOneListener)
		var org any
		if err := json.Unmarshal([]byte(scpLicense(c).String), &org); err != nil {
			c.Fatalf("decoding the org license: %v", err)
		}
		hsDoc, _, _ := scpServedOK(c, *sc.Token)
		var polled scpServed
		c.Sidecar(*sc.Token).Do(c, http.MethodGet, "/sidecars/configuration", nil).Expect(c, http.StatusOK).JSON(c, &polled)
		for route, doc := range map[string]scpServed{"handshake": hsDoc, "configuration": polled} {
			var served any
			if doc.License == nil || json.Unmarshal([]byte(*doc.License), &served) != nil {
				c.Fatalf("%s: the license key is not the org's license document: %v", route, doc.License)
			}
			a, _ := json.Marshal(served)
			b, _ := json.Marshal(org)
			if !bytes.Equal(a, b) {
				c.Fatalf("%s served license %s, want the org's %s", route, truncate(a), truncate(b))
			}
		}

		withLicense := `{"license": "per-sidecar", "listeners": [{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"}]}`
		c.Admin().Do(c, http.MethodPost, "/sidecars",
			map[string]any{"name": c.Name("own"), "configuration": json.RawMessage(withLicense)}).Expect(c, http.StatusUnprocessableEntity)
		scpPut(c, name, withLicense).Expect(c, http.StatusUnprocessableEntity)
		c.Admin().Do(c, http.MethodGet, "/sidecars/"+c.Name("own"), nil).Expect(c, http.StatusNotFound)
		if got := scpGet(c, name); bytes.Contains(got.Configuration, []byte(`"license"`)) {
			c.Fatalf("the admin view of a sidecar exposes a license key: %s", truncate(got.Configuration))
		}
	},
})
