package kit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

type uiUser struct {
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
}

// uiTwin has every shape a state document takes: scalars, a list of
// objects, an empty list, a list of strings, a map and a time.
type uiTwin struct{ fakeTwin }

func (u *uiTwin) Name() string { return "shapes" }
func (u *uiTwin) State() any {
	return struct {
		Issuer  string            `json:"issuer"`
		TTL     int               `json:"token_ttl_sec"`
		Users   []uiUser          `json:"users"`
		Codes   []uiUser          `json:"codes"`
		Expired []string          `json:"expired_tokens"`
		Routes  map[string]string `json:"routes"`
		At      time.Time         `json:"at"`
	}{
		Issuer: "https://oidc-twin", TTL: 3600,
		Users: []uiUser{
			{Email: "alice@example.com", Name: "Alice", Groups: []string{"sre"}},
			{Email: "bob@example.com", Name: "<b>Bob</b>", Groups: []string{"admin", "sre"}},
		},
		Codes:   []uiUser{},
		Expired: []string{"id_token"},
		Routes:  map[string]string{"oidc-twin": "127.0.0.1:18441"},
		At:      time.Date(2026, 10, 19, 12, 34, 56, 0, time.UTC),
	}
}

func TestUI(t *testing.T) {
	srv := httptest.NewServer(Handler(&uiTwin{}))
	defer srv.Close()
	if code, _ := do(t, srv, "POST", "/_twin/faults", `{"path":"/jwks","status":503}`); code != http.StatusNoContent {
		t.Fatalf("add fault: %d", code)
	}
	resp, err := srv.Client().Get(srv.URL + "/_twin/ui")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q, want text/html", ct)
	}
	_, page := do(t, srv, "GET", "/_twin/ui", "")

	// Sections in document order, then the faults.
	order := regexp.MustCompile(`data-section="([a-z_]+)"`).FindAllStringSubmatch(page, -1)
	var got []string
	for _, m := range order {
		got = append(got, m[1])
	}
	if want := "settings users codes expired_tokens routes faults"; strings.Join(got, " ") != want {
		t.Fatalf("sections %v, want %s", got, want)
	}
	for name, want := range map[string]string{
		"a scalar is a setting":            `<dt>issuer</dt><dd data-key="issuer">https://oidc-twin</dd>`,
		"a time shows its clock time":      `<time datetime="2026-10-19T12:34:56Z" title="2026-10-19T12:34:56Z">12:34:56</time>`,
		"columns follow the field order":   `<th>email</th><th>name</th><th>groups</th>`,
		"a list of strings in a row":       `<td><span class="chip">admin</span><span class="chip">sre</span></td>`,
		"state is escaped":                 `<td>&lt;b&gt;Bob&lt;/b&gt;</td>`,
		"an empty list is a section":       `<h2>Codes <span class="count">0</span></h2>` + "\n" + `<p class="empty">none</p>`,
		"a list of strings is chips":       `<div class="chips"><span class="chip">id_token</span></div>`,
		"a map is pairs":                   `<dt>oidc-twin</dt><dd>127.0.0.1:18441</dd>`,
		"faults show their hits":           `<td>/jwks</td><td>503</td>`,
		"the page refreshes itself":        `setInterval(`,
		"the page links the state as JSON": `href="/_twin/state"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("%s: the page lacks %s", name, want)
		}
	}
	if strings.Contains(page, "<b>Bob</b>") {
		t.Error("a state value must not inject HTML")
	}
}

// The order of a map's keys comes from encoding/json, which sorts them.
func TestDecodeOrderedKeepsDocumentOrder(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(`{"b":1,"a":[{"z":true,"y":null}],"c":{}}`))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(object)
	if len(o) != 3 || o[0].Key != "b" || o[1].Key != "a" || o[2].Key != "c" {
		t.Fatalf("keys out of order: %+v", o)
	}
	row := o[1].Val.([]any)[0].(object)
	if row[0].Key != "z" || row[1].Key != "y" || row[1].Val != nil {
		t.Fatalf("nested keys out of order: %+v", row)
	}
	if got := compact(o); got != `{"a":[{"y":null,"z":true}],"b":1,"c":{}}` {
		t.Fatalf("compact: %s", got)
	}
}
