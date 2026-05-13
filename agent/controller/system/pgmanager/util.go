package pgmanager

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"text/template"
)

const (
	typeManaged  = "managed"
	typeExternal = "external"
)

// ---------------------------------------------------------------------------
// Quoting helpers used by the templates.
// ---------------------------------------------------------------------------

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// quoteIdent quotes a SQL identifier per Postgres rules: wrap in double
// quotes, double any embedded double quotes. Used for role names,
// database names, schema names, table names — anywhere we interpolate
// a name into a DDL statement.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

var funcs = template.FuncMap{
	"quoteLiteral": quoteLiteral,
	// quoteIdent is registered for the sqlPlanTmpl, which uses it to
	// quote role names, database names, schema names — every literal
	// identifier that goes into the emitted SQL.
	"quoteIdent": quoteIdent,
	// join takes a separator and a string slice and returns the
	// joined result. Used in sqlPlanTmpl to comma-join privilege
	// keyword lists for GRANT/REVOKE ON ALL TABLES.
	"join": func(sep string, items []string) string {
		return strings.Join(items, sep)
	},
}

func render(name, src string, data any) (string, error) {
	t, err := template.New(name).Funcs(funcs).Parse(src)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ConnectionParts is the components of a Postgres connection URI,
// extracted from the YAML's adminURI so the rest of the code can
// rebuild a URI per database without doing string substitution on the
// path. Holding components separately also makes the call sites
// inspectable: a test or a logging line can read User and Host without
// re-parsing.
//
// DefaultDB is the database segment of the original URI (typically
// "postgres") and is used for connections that aren't fanning out to a
// specific managed database — the cluster query, the parent-role
// existence check.
//
// Options is the URI's query string parsed into a flat map. Repeated
// keys are joined with the last value winning, which is fine for the
// flags we care about (sslmode, application_name, connect_timeout).
// We use a flat map[string]string rather than url.Values because the
// Postgres URI conventions don't use multi-valued options.
type ConnectionParts struct {
	Host      string
	Port      string // string to keep "" distinct from "0"; rendered as-is
	User      string
	Password  string
	DefaultDB string
	Options   map[string]string
}

// connURI rebuilds a connection URI from the components, targeting the
// given database name. The database name is path-escaped so unusual
// characters (rare but possible in db names) don't break the URI.
func (c ConnectionParts) connURI(db string) string {
	u := url.URL{
		Scheme: "postgres",
		Host:   c.Host,
		Path:   "/" + db,
	}
	if c.Port != "" {
		u.Host = c.Host + ":" + c.Port
	}
	switch {
	case c.User != "" && c.Password != "":
		u.User = url.UserPassword(c.User, c.Password)
	case c.User != "":
		u.User = url.User(c.User)
	}
	if len(c.Options) > 0 {
		q := url.Values{}
		// Sort the keys for stable output; helps when comparing
		// connection strings in tests or logs.
		keys := make([]string, 0, len(c.Options))
		for k := range c.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			q.Set(k, c.Options[k])
		}
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// defaultAttributes returns the attribute set we apply to managed and
// external roles alike — LOGIN/INHERIT on, everything else off.
func defaultAttributes() map[string]bool {
	return map[string]bool{
		"LOGIN":       true,
		"INHERIT":     true,
		"SUPERUSER":   false,
		"BYPASSRLS":   false,
		"CREATEDB":    false,
		"CREATEROLE":  false,
		"REPLICATION": false,
	}
}

// randomPassword generates a 24-byte URL-safe random string, ~32 chars
// after base64 encoding. The output appears verbatim in apply.sql, so
// the admin can capture it before executing the file.
func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ---------------------------------------------------------------------------
// Path expansion: turn a YAML entry into a desired Snapshot.
// ---------------------------------------------------------------------------

// schemasOfInterest groups the (db, schema) pairs the config references.
// Bare "<db>" paths are normalized to "<db>.public".
func schemasOfInterest(scopes []string) map[string][]string {
	bySchema := map[string]map[string]struct{}{}
	for _, p := range scopes {
		db, schema, _ := splitPath(p)
		if db == "" {
			continue
		}
		if _, ok := bySchema[db]; !ok {
			bySchema[db] = map[string]struct{}{}
		}
		bySchema[db][schema] = struct{}{}
	}
	out := map[string][]string{}
	for db, set := range bySchema {
		schemas := make([]string, 0, len(set))
		for s := range set {
			schemas = append(schemas, s)
		}
		sort.Strings(schemas)
		out[db] = schemas
	}
	return out
}

// splitPath splits a dotted path of 1, 2, or 3 segments into
// (db, schema, table). 1-segment paths default schema to "public" and
// leave table empty. 2-segment paths leave table empty. Returns empty
// db on malformed input.
func splitPath(p string) (db, schema, table string) {
	parts := strings.Split(p, ".")
	switch len(parts) {
	case 1:
		return parts[0], "public", ""
	case 2:
		return parts[0], parts[1], ""
	case 3:
		return parts[0], parts[1], parts[2]
	default:
		return "", "", ""
	}
}

// splitDBSchema splits "shop.public" into ("shop", "public"). For names
// containing dots we'd need quoting, but our snapshot is generated from
// system catalog names which are simple identifiers.
func splitDBSchema(s string) (string, string) {
	i := strings.IndexByte(s, '.')
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func strSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func checksumSha256(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
