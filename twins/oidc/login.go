package oidc

import (
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

type loginParam struct{ Name, Value string }

type loginUser struct {
	User
	Initial string
}

type loginView struct {
	// Action is the authorize endpoint discovery advertises, path prefix
	// included.
	Action   string
	Issuer   string
	ClientID string
	Redirect string
	Params   []loginParam
	Users    []loginUser
}

// loginPage lists the seeded users. Each one is a button that sends the
// same authorize request with login_hint, so the page holds no state and
// the approval stays in authorize. The caller holds t.mu.
func (t *Twin) loginPage(w http.ResponseWriter, q url.Values) {
	v := loginView{Action: t.authorizeURL(), Issuer: t.issuer, ClientID: t.cfg.ClientID}
	if u, err := url.Parse(q.Get("redirect_uri")); err == nil {
		v.Redirect = u.Host
	}
	names := make([]string, 0, len(q))
	for k := range q {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		for _, val := range q[k] {
			v.Params = append(v.Params, loginParam{Name: k, Value: val})
		}
	}
	for _, u := range t.sortedUsers() {
		name := u.Name
		if name == "" {
			name = u.Email
		}
		// The first rune, not the first byte: "Élodie" shows "É".
		r, _ := utf8.DecodeRuneInString(name)
		v.Users = append(v.Users, loginUser{User: u, Initial: strings.ToUpper(string(r))})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := loginTemplate.Execute(w, v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in · OIDC twin</title>
<style>
:root { color-scheme: light dark; --bg: #f2f4f7; --card: #fff; --text: #1c2024; --dim: #60646c; --line: #e4e6eb; --chip: #eef0f4; --hover: #f5f7ff; --accent: #3e63dd; }
@media (prefers-color-scheme: dark) { :root { --bg: #111317; --card: #1a1d23; --text: #e8eaee; --dim: #9aa0aa; --line: #2a2e36; --chip: #262a32; --hover: #20252f; --accent: #8da4ef; } }
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 24px 16px; background: var(--bg); color: var(--text); font: 14px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif; }
main { width: 100%; max-width: 440px; background: var(--card); border: 1px solid var(--line); border-radius: 12px; padding: 28px; }
.tag { color: var(--dim); font-size: 12px; text-transform: uppercase; letter-spacing: .06em; }
h1 { margin: 4px 0 6px; font-size: 22px; }
p { margin: 0 0 18px; color: var(--dim); }
form { display: grid; gap: 8px; }
button { display: flex; align-items: center; gap: 12px; width: 100%; padding: 10px 12px; text-align: left; font: inherit; color: inherit; background: none; border: 1px solid var(--line); border-radius: 8px; cursor: pointer; }
button:hover, button:focus-visible { background: var(--hover); border-color: var(--accent); outline: none; }
.avatar { flex: none; width: 36px; height: 36px; border-radius: 50%; display: grid; place-items: center; background: var(--accent); color: #fff; font-weight: 600; }
.who { flex: 1; min-width: 0; }
.who b { display: block; }
.who small { color: var(--dim); }
.groups { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 4px; }
.chip { padding: 0 8px; border-radius: 10px; background: var(--chip); font-size: 12px; }
.empty { padding: 12px; border: 1px dashed var(--line); border-radius: 8px; }
footer { margin-top: 18px; color: var(--dim); font-size: 12px; }
code { font: 12px ui-monospace, Menlo, monospace; }
</style>
</head>
<body>
<main>
<span class="tag">OIDC twin</span>
<h1>Sign in</h1>
<p>Pick a seeded user. The twin approves with no password.</p>
<form method="get" action="{{.Action}}">
{{- range .Params}}
<input type="hidden" name="{{.Name}}" value="{{.Value}}">
{{- end}}
{{- range .Users}}
<button type="submit" name="login_hint" value="{{.Email}}" data-user="{{.Email}}">
<span class="avatar">{{.Initial}}</span>
<span class="who"><b>{{.Name}}</b><small>{{.Email}}</small></span>
<span class="groups">{{range .Groups}}<span class="chip">{{.}}</span>{{end}}</span>
</button>
{{- else}}
<p class="empty">No users seeded. <code>POST /_twin/seed</code> adds them.</p>
{{- end}}
</form>
<footer>Issuer <code>{{.Issuer}}</code> · client <code>{{.ClientID}}</code>{{if .Redirect}} · returns to <code>{{.Redirect}}</code>{{end}}</footer>
</main>
</body>
</html>
`))
