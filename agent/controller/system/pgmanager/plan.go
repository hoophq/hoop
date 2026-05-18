package pgmanager

import (
	"fmt"
	"sort"
)

// ---------------------------------------------------------------------------
// SQL plan generator.
//
// Order:
//   1. CREATE ROLE (if missing). External-mode roles get IN ROLE <parent>
//      appended so the parent membership attaches atomically.
//   2. ALTER ROLE attribute additions (additive-only).
//   3. Membership reconciliation: revoke any membership in current that
//      isn't in desired, and grant any membership in desired that isn't
//      in current. Under managed mode desired is empty, so this collapses
//      to "revoke everything." Under external mode, desired contains the
//      parent role from source_role.
//   4. Per-database, per-scope, in a transaction:
//        a. GRANT CONNECT to the database (if missing).
//        b. GRANT USAGE on the schema (if missing).
//        c. Bulk privilege reconciliation: GRANT/REVOKE … ON ALL TABLES
//           IN SCHEMA … to bring the schema's bulk privileges into line
//           with desired.
//
// CONNECT / USAGE / attributes are additive-only (never revoked).
// Per-table state isn't managed: when a scope's status is
// `out-of-sync/drifted`, the snapshot surfaces the deviating tables
// in current.yaml for inspection but apply.sql operates only at the
// bulk level.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// SQL plan structure.
//
// buildSQLPlan computes the diff between desired and current and packs
// the result into a sqlPlan value. The plan's Render method walks that
// value and emits the SQL via a Go template. Splitting the two
// responsibilities has three benefits:
//
//   - the diff logic (which is real algorithm: set-differences, additive
//     reconciliation, empty-schema handling) stays in Go where it can
//     be reasoned about and tested.
//   - the SQL emission (which is mostly conditional layout: "emit this
//     line if that field is non-empty") moves into a template, where
//     conditionals read as `{{ if … }}` blocks rather than `if …` /
//     `b.WriteString(...)` scaffolding.
//   - the plan structure is inspectable on its own. A test or a future
//     dry-run command can verify "given these inputs, the planner
//     decides X" without rendering SQL.
// ---------------------------------------------------------------------------

// sqlPlan is the rendered representation of a reconcile plan.
//
// Cluster-level work (CreateRole, AlterAttributes, GrantMemberships,
// RevokeMemberships) runs outside any transaction because role-state
// changes affect the catalog at a level above any single database.
//
// Per-database work runs inside one BEGIN/COMMIT block per database,
// so that all changes within a database succeed or fail atomically.
type sqlPlan struct {
	Role string

	// CreateRole is non-nil when the role doesn't exist in current
	// state. The template emits a CREATE ROLE statement using this
	// spec; otherwise it skips to ALTER attributes / memberships.
	CreateRole *createRoleSpec

	// AlterAttributes lists role attribute keywords (LOGIN, INHERIT, …)
	// that need flipping from false to true. Empty when the role
	// either doesn't exist (those attributes ride along on CREATE
	// ROLE) or already has every desired attribute. Additive only —
	// the planner doesn't drop attributes the cluster has but desired
	// doesn't list.
	AlterAttributes []string

	// GrantMemberships and RevokeMemberships hold parent-role
	// memberships to add/remove. Skipped entirely when the role was
	// just created (CREATE ROLE … IN ROLE handles initial parent
	// attachment atomically).
	GrantMemberships  []string
	RevokeMemberships []string

	// RotatePassword is non-nil when the role exists and its
	// password_version differs from desired. The template emits an
	// ALTER ROLE … PASSWORD '<new>' plus a COMMENT ON ROLE update to
	// re-stamp the version. nil when no rotation is pending — either
	// because the role doesn't exist (the CREATE ROLE path handles
	// initial password + comment atomically) or because the versions
	// already match.
	RotatePassword *rotatePasswordSpec

	// Databases is per-database work. Ordering is alphabetical for
	// reproducibility.
	Databases []databasePlan
}

// rotatePasswordSpec drives the ALTER ROLE PASSWORD path. Used only
// when the operator has set Config.RotatePassword = true AND the role
// already exists. Asking to rotate a non-existent role is rejected
// earlier (see buildSQLPlan).
type rotatePasswordSpec struct {
	// Password is the literal SQL-quoted new password (random,
	// freshly generated). Captured by the operator from apply.sql.
	Password string
}

// createRoleSpec drives the CREATE ROLE statement when the role
// doesn't exist yet.
type createRoleSpec struct {
	Password string
	// Attributes are the attribute keywords to attach to CREATE ROLE.
	// Only desired-true attributes appear; the others are implicit
	// (Postgres defaults all role attributes to false).
	Attributes []string
	// InRole is the list of parent roles to attach via IN ROLE.
	// Empty for managed-mode roles; populated for external-mode
	// roles where source_role binds the child to its parent.
	InRole []string
}

// databasePlan is per-database work — one transaction's worth.
type databasePlan struct {
	Name string
	// GrantConnect is true when at least one scope in this database
	// is desired and current lacks CONNECT. Deduplicated across
	// scopes so each database emits at most one GRANT CONNECT
	// statement per plan run.
	GrantConnect bool
	// Scopes is the per-(schema) work inside this database.
	Scopes []scopePlan
}

// scopePlan is per-schema work inside one database.
type scopePlan struct {
	Schema string
	// GrantUsage is true when desired wants USAGE and current lacks
	// it. Additive only.
	GrantUsage bool
	// BulkGrants and BulkRevokes are privilege keywords for the
	// `GRANT … ON ALL TABLES IN SCHEMA …` and corresponding REVOKE.
	// Order: REVOKE before GRANT, so the role's effective privilege
	// set narrows before it widens.
	BulkGrants  []string
	BulkRevokes []string
}

// IsEmpty reports whether the plan would produce any SQL. The
// template calls this to decide between rendering `-- no changes` and
// the full apply.sql body. Exported so text/template's method lookup
// can find it ({{ if .IsEmpty }}).
func (p sqlPlan) IsEmpty() bool {
	if p.CreateRole != nil {
		return false
	}
	if len(p.AlterAttributes) > 0 {
		return false
	}
	if len(p.GrantMemberships) > 0 || len(p.RevokeMemberships) > 0 {
		return false
	}
	if p.RotatePassword != nil {
		return false
	}
	for _, d := range p.Databases {
		if d.GrantConnect {
			return false
		}
		for _, s := range d.Scopes {
			if s.GrantUsage || len(s.BulkGrants) > 0 || len(s.BulkRevokes) > 0 {
				return false
			}
		}
	}
	return true
}

// buildSQLPlan computes the reconcile plan from desired and current
// Snapshots. It does the diff logic but emits no SQL — that's the
// plan's Render method.
//
// rotateRequested comes straight from Config.RotatePassword. It's
// passed as a parameter rather than carried on a Snapshot because
// it's an imperative ("the operator wants this run to rotate the
// password"), not observable state — there's nothing in the cluster
// that says "an operator wanted rotation."
func buildSQLPlan(rotateRequested bool, desired, current *Snapshot) (sqlPlan, error) {
	plan := sqlPlan{Role: desired.Role}

	// --- 1. CREATE ROLE if missing -----------------------------------------
	if !current.Exists {
		pw, err := randomPassword()
		if err != nil {
			return sqlPlan{}, fmt.Errorf("generate password: %w", err)
		}
		spec := &createRoleSpec{
			Password: pw,
		}
		// Attribute keywords for true-valued desired attributes.
		for _, k := range sortedKeys(desired.Attributes) {
			if desired.Attributes[k] {
				spec.Attributes = append(spec.Attributes, k)
			}
		}
		// External-mode roles attach to their parent at creation time
		// via IN ROLE — one atomic statement instead of CREATE ROLE
		// followed by GRANT parent TO child.
		if len(desired.Memberships) > 0 {
			spec.InRole = append(spec.InRole, desired.Memberships...)
		}
		plan.CreateRole = spec
	}

	// --- 2. ALTER ROLE attribute additions ---------------------------------
	// Only flip false→true (additive). Skip if the role was just created
	// (CREATE ROLE already set them).
	if current.Exists {
		for _, k := range sortedKeys(desired.Attributes) {
			want := desired.Attributes[k]
			if !want {
				continue
			}
			if !current.Attributes[k] {
				plan.AlterAttributes = append(plan.AlterAttributes, k)
			}
		}
	}

	// --- 3. Membership reconciliation -------------------------------------
	// Set-difference: revoke any membership the role has that desired
	// doesn't list, and grant any membership desired lists that the role
	// doesn't already have. Skipped entirely when the role was just
	// created — CREATE ROLE IN ROLE already attached the parent.
	if current.Exists {
		wantMembers := strSet(desired.Memberships)
		haveMembers := strSet(current.Memberships)
		for m := range wantMembers {
			if _, ok := haveMembers[m]; !ok {
				plan.GrantMemberships = append(plan.GrantMemberships, m)
			}
		}
		for m := range haveMembers {
			if _, ok := wantMembers[m]; !ok {
				plan.RevokeMemberships = append(plan.RevokeMemberships, m)
			}
		}
		sort.Strings(plan.GrantMemberships)
		sort.Strings(plan.RevokeMemberships)
	}

	// --- 4. Password rotation ----------------------------------------------
	// Trigger: operator set Config.RotatePassword = true. The role must
	// exist (validated at the top of this function); CREATE ROLE handles
	// initial password assignment in step 1.
	//
	// Model A: one-shot trigger, operator-cleared. The boolean stays
	// true across plan runs until the operator flips it back to false
	// in YAML. Each run with the flag set produces a different random
	// password. That's the deliberate safety property: leaving the
	// flag stuck produces visible noise (a new password every plan)
	// rather than silently rotating once and then ignoring further
	// rotations the operator might genuinely intend.
	if rotateRequested {
		pw, err := randomPassword()
		if err != nil {
			return sqlPlan{}, fmt.Errorf("generate password for rotation: %w", err)
		}
		plan.RotatePassword = &rotatePasswordSpec{
			Password: pw,
		}
	}

	// --- 5. Per-scope reconciliation, batched per database -----------------
	allKeys := map[string]struct{}{}
	for k := range desired.ScopeStates {
		allKeys[k] = struct{}{}
	}
	for k := range current.ScopeStates {
		allKeys[k] = struct{}{}
	}

	byDB := map[string]*databasePlan{}
	addDB := func(db string) *databasePlan {
		if d, ok := byDB[db]; ok {
			return d
		}
		d := &databasePlan{Name: db}
		byDB[db] = d
		return d
	}

	for _, key := range sortedSet(allKeys) {
		db, schema := splitDBSchema(key)
		if db == "" {
			continue
		}
		want, isWanted := desired.ScopeStates[key]
		have, exists := current.ScopeStates[key]

		sp := scopePlan{Schema: schema}

		// CONNECT (per-db, deduped). Additive only — emit when desired
		// wants it and current lacks it.
		if isWanted && want.Connect && (!exists || !have.Connect) {
			addDB(db).GrantConnect = true
		}

		// USAGE (per-scope). Additive only — never revoke.
		if isWanted && want.Usage && (!exists || !have.Usage) {
			sp.GrantUsage = true
		}

		// Bulk privileges: set difference. Skip the diff entirely
		// when the live schema is empty — `GRANT … ON ALL TABLES`
		// against zero tables is a no-op, and emitting it on every
		// plan run would add noise. The grant gets emitted naturally
		// once tables exist (because the rollup will then differ from
		// the desired set in a non-vacuous way).
		emptySchema := exists && have.TableCount == 0
		if !emptySchema {
			var wantBulk, haveBulk map[string]struct{}
			if isWanted {
				wantBulk = strSet(want.BulkPrivileges)
			} else {
				wantBulk = map[string]struct{}{}
			}
			if exists {
				haveBulk = strSet(have.BulkPrivileges)
			} else {
				haveBulk = map[string]struct{}{}
			}
			for p := range wantBulk {
				if _, ok := haveBulk[p]; !ok {
					sp.BulkGrants = append(sp.BulkGrants, p)
				}
			}
			for p := range haveBulk {
				if _, ok := wantBulk[p]; !ok {
					sp.BulkRevokes = append(sp.BulkRevokes, p)
				}
			}
			sort.Strings(sp.BulkGrants)
			sort.Strings(sp.BulkRevokes)
		}

		// Skip recording a scope where nothing needs to change.
		if !sp.GrantUsage && len(sp.BulkGrants) == 0 && len(sp.BulkRevokes) == 0 {
			continue
		}

		addDB(db).Scopes = append(addDB(db).Scopes, sp)
	}

	// Materialize databases in alphabetical order; sort scopes inside
	// each for determinism.
	dbNames := make([]string, 0, len(byDB))
	for db := range byDB {
		dbNames = append(dbNames, db)
	}
	sort.Strings(dbNames)
	for _, db := range dbNames {
		d := byDB[db]
		sort.Slice(d.Scopes, func(i, j int) bool { return d.Scopes[i].Schema < d.Scopes[j].Schema })
		plan.Databases = append(plan.Databases, *d)
	}

	return plan, nil
}

// sqlPlanTmpl renders a sqlPlan to perform the migration in the database
//
// All conditional emission is in the template ({{ if … }}, {{ with … }},
// {{ range … }}); the diff itself is computed by buildSQLPlan.
// quoteIdent and join are the template funcs needed for rendering —
// registered in funcs (see render()).
//
// Note: sqlPlan has a Render() method (which executes this template).
// Go templates can call any exported zero-arg method on the data
// they're handed, so writing `{{ .Render }}` anywhere in this template
// would recurse infinitely. We use `{{ .IsEmpty }}` because IsEmpty
// is a side-effect-free predicate; do NOT add `{{ .Render }}`.
const sqlPlanTmpl = `{{ if .IsEmpty -}}
-- no changes
{{ else -}}

-- =========================================================
-- Role: {{ .Role }}
-- =========================================================

{{ with .CreateRole -}}
-- Role does not exist yet; create it with the desired attributes.
-- The password below is randomly generated and replaced in runtime to avoid logging and leaking it
CREATE ROLE {{ $.Role | quoteIdent }} WITH PASSWORD 'ROLE_PASSWORD_PLACEHOLDER'
    {{- range .Attributes }} {{ . }}{{ end }}
    {{- if .InRole }} IN ROLE
        {{- range $i, $r := .InRole }}{{ if $i }},{{ end }} {{ $r | quoteIdent }}{{ end }}
    {{- end }};

{{ end -}}

{{- with .AlterAttributes }}
-- Cluster-level role attribute additions (additive-only).
ALTER ROLE {{ $.Role | quoteIdent }}
    {{- range . }} {{ . }}{{ end }};

{{ end -}}

{{- if .RevokeMemberships }}
-- Memberships in current state that are not in desired: revoke.
{{- range .RevokeMemberships }}
REVOKE {{ . | quoteIdent }} FROM {{ $.Role | quoteIdent }};
{{- end }}

{{ end -}}

{{- if .GrantMemberships }}
-- Memberships in desired state that are not in current: grant.
{{- range .GrantMemberships }}
GRANT {{ . | quoteIdent }} TO {{ $.Role | quoteIdent }};
{{- end }}

{{ end -}}

{{- with .RotatePassword }}
-- Password rotation: operator set rotate_password: true in YAML.
-- Capture the new password from this file before applying. To stop
-- rotating on subsequent plans, set rotate_password back to false.
ALTER ROLE {{ $.Role | quoteIdent }} WITH PASSWORD 'ROLE_PASSWORD_PLACEHOLDER';

{{ end -}}

{{- range .Databases }}
-- =========================================================
-- Database: {{ .Name }}
-- =========================================================
\connect {{ .Name | quoteIdent }}
BEGIN;
{{- if .GrantConnect }}
GRANT CONNECT ON DATABASE {{ .Name | quoteIdent }} TO {{ $.Role | quoteIdent }};
{{- end }}
{{- range .Scopes }}
    {{- if .GrantUsage }}
GRANT USAGE ON SCHEMA {{ .Schema | quoteIdent }} TO {{ $.Role | quoteIdent }};
    {{- end }}
    {{- if .BulkRevokes }}
REVOKE {{ .BulkRevokes | join ", " }} ON ALL TABLES IN SCHEMA {{ .Schema | quoteIdent }} FROM {{ $.Role | quoteIdent }};
    {{- end }}
    {{- if .BulkGrants }}
GRANT {{ .BulkGrants | join ", " }} ON ALL TABLES IN SCHEMA {{ .Schema | quoteIdent }} TO {{ $.Role | quoteIdent }};
    {{- end }}
{{- end }}
COMMIT;

{{ end -}}
{{- end }}`

// Render executes sqlPlanTmpl against the plan, producing the full
// apply.sql content (including preamble and the no-changes branch).
// Pure formatting — no diff logic.
//
// As a method, callers read as `plan.Render()` rather than passing
// the plan into a free function: the rendering is conceptually a
// property of the plan, not an external operation on it. Exported
// because of Go's convention, not because anything outside the
// package calls it (the package is main).
func (p sqlPlan) Render() (string, error) {
	return render("sql_plan", sqlPlanTmpl, p)
}
