// Port of CLJS webapp.jira-templates.cascade: the field-config-driven cascade
// of the CMDB dropdowns.
//
// Each Assets custom field carries its own AQL configuration in Jira (the same
// one the JSM portal applies): an object scope filter plus an optional issue
// scope filter that may reference sibling fields with
// ${customfield_x.label} placeholders. `fieldconfigs` maps jira_field to
// { objectSchemaId, objectFilter, issueScope }; a field absent from it never
// filters and never cascades.
//
// A row is a template CMDB item plus what the prompt learned: `jira_values`
// (loaded options), `value` (the preset name, then the picked object id) and
// `selectedName` (the picked object's name).

// The row waits on an upstream pick: show no options.
export const PENDING = 'pending'

const PLACEHOLDER = /\$\{(customfield_\d+)(?:\.([A-Za-z]+))?\}/g

const isBlank = (s) => s == null || String(s).trim() === ''

// API items [{ jira_field, object_schema_id, object_filter_query,
// issue_scope_filter_query }] -> { [jira_field]: { objectSchemaId,
// objectFilter, issueScope } }.
export function indexConfigs(items = []) {
  const configs = {}
  for (const item of items) {
    configs[item.jira_field] = {
      objectSchemaId: item.object_schema_id,
      objectFilter: item.object_filter_query,
      issueScope: item.issue_scope_filter_query,
    }
  }
  return configs
}

// The name captured at selection time, then a lookup of the selected id in the
// loaded values, then the raw value (a plain name when prefilled by the
// template).
function selectedName(row) {
  if (row.selectedName != null) return row.selectedName
  const match = (row.jira_values ?? []).find((v) => v.id === row.value)
  if (match?.name != null) return match.name
  return isBlank(row.value) ? null : row.value
}

// AQL string literal: backslashes before quotes, so a trailing backslash cannot
// swallow the closing quote.
function quoteAql(s) {
  return `"${String(s).replaceAll('\\', '\\\\').replaceAll('"', '\\"')}"`
}

// The quoted substitution of one placeholder, or null while the row it names
// has no selection. `label` (or no suffix) takes the name; any other suffix
// takes the raw value.
function placeholderValue(rows, field, suffix) {
  const row = rows.find((r) => r.jira_field === field)
  if (!row) return null
  const raw = suffix === undefined || suffix === 'label' ? selectedName(row) : row.value
  return isBlank(raw) ? null : quoteAql(raw)
}

// Null when any placeholder is unresolved: a partly substituted filter would
// show wrong options.
function substitutePlaceholders(aql, rows) {
  let unresolved = false
  const resolved = aql.replace(PLACEHOLDER, (_, field, suffix) => {
    const value = placeholderValue(rows, field, suffix)
    if (value === null) unresolved = true
    return value ?? ''
  })
  return unresolved ? null : resolved
}

// How to scope a row's dropdown:
//   null          - no Assets configuration: keep the template's object type.
//   PENDING       - its dependent filter has no upstream pick yet.
//   { aql, objectSchemaId }
//                 - the object scope filter AND the resolved issue scope; it
//                   replaces the template's object type.
export function filterFor(row, rows, fieldconfigs) {
  const config = fieldconfigs[row.jira_field]
  if (!config) return null
  const { objectSchemaId, objectFilter, issueScope } = config
  const scope = isBlank(issueScope) ? null : substitutePlaceholders(issueScope, rows)
  // An unresolved dependent filter wins over a static scope: Jira enforces
  // both filters on the field.
  if (!isBlank(issueScope) && scope === null) return PENDING
  const clauses = [objectFilter, scope].filter((c) => !isBlank(c))
  if (clauses.length === 0) return null
  return {
    aql: clauses.length === 1 ? clauses[0] : clauses.map((c) => `(${c})`).join(' AND '),
    objectSchemaId,
  }
}

function references(fieldconfigs, row, changedField) {
  const issueScope = fieldconfigs[row.jira_field]?.issueScope ?? ''
  return [...String(issueScope).matchAll(PLACEHOLDER)].some((m) => m[1] === changedField)
}

// Rows whose issue scope references the changed row's field.
export function dependentsOf(changed, rows, fieldconfigs) {
  return rows.filter(
    (r) => r.jira_field !== changed.jira_field && references(fieldconfigs, r, changed.jira_field),
  )
}

// The transitive closure of dependentsOf, breadth-first: a cleared row clears
// its own dependents in turn. The seen set ends a cycle.
export function allDependents(changed, rows, fieldconfigs) {
  const frontier = [changed]
  const seen = new Set([changed.jira_field])
  const acc = []
  while (frontier.length) {
    const row = frontier.shift()
    const deps = dependentsOf(row, rows, fieldconfigs).filter((d) => !seen.has(d.jira_field))
    for (const d of deps) {
      seen.add(d.jira_field)
      frontier.push(d)
      acc.push(d)
    }
  }
  return acc
}
