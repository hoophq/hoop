import { allDependents } from './cascade'

// Port of CLJS webapp.jira-templates.form-data: what the prompt form starts
// with, what a CMDB pick changes and what Send posts.
//
// `fields` is the form's jira_fields. A prompt answer is its input's string; a
// CMDB pick is { value: objectId, label: name }. The gateway reads CMDB picks
// from jira_fields too: nothing sends cmdb_fields (gateway/jira/issuesapi.go).

const isPick = (v) => v !== null && typeof v === 'object'

// The gateway reads `required` as the text "true" (gateway/models/jira_issue_templates.go).
export const isRequired = (field) => String(field.required) === 'true'

// A CMDB preset is an object NAME; the form starts with the object it names,
// when the loaded values hold it.
export function initFields(rows) {
  const fields = {}
  for (const { jira_field, jira_values, value } of rows) {
    const match = (jira_values ?? []).find((v) => v.name === value)
    if (match) fields[jira_field] = { value: match.id, label: match.name }
  }
  return fields
}

// A pick drops the picks of the rows whose AQL filter it changes.
export function selectCmdb(fields, row, rows, fieldconfigs, pick) {
  const next = { ...fields, [row.jira_field]: pick }
  for (const dep of allDependents(row, rows, fieldconfigs)) delete next[dep.jira_field]
  return next
}

// datetime-local holds a wall-clock time; Jira gets the instant.
function toIsoWhenDatetime(prompts, jiraField, value) {
  const prompt = prompts.find((p) => p.jira_field === jiraField)
  if (prompt?.field_type === 'datetime-local' && value != null && value !== '') {
    return new Date(value).toISOString()
  }
  return value
}

// The body part Send posts.
export function submitFields(prompts, fields) {
  const jiraFields = {}
  for (const [jiraField, value] of Object.entries(fields)) {
    jiraFields[jiraField] = isPick(value) ? value.value : toIsoWhenDatetime(prompts, jiraField, value)
  }
  return { jira_fields: jiraFields }
}

// The required prompts and CMDB rows that have no answer.
export function missingRequired(prompts, rows, fields) {
  return [...prompts, ...rows]
    .filter(isRequired)
    .filter(({ jira_field }) => {
      const v = fields[jira_field]
      return v == null || v === '' || (isPick(v) && (v.value == null || v.value === ''))
    })
    .map(({ jira_field }) => jira_field)
}

// The user must answer when the template asks something, or when a CMDB row
// has no preset the loaded values hold.
export function needsForm(prompts, rows, fields) {
  return prompts.length > 0 || rows.some((r) => fields[r.jira_field] == null)
}
