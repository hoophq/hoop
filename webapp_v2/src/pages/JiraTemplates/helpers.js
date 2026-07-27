// Constants and transforms for Jira Templates, ported from the legacy
// CLJS implementation (webapp/src/webapp/jira_templates/helpers.cljs and
// mapping_table.cljs). The API payload shapes must match JiraIssueTemplateRequest.

export const CONNECTION_TAG_PREFIX = 'session.connection_tags.'

export const MAPPING_TYPE_OPTIONS = [
  { value: 'preset', label: 'Preset' },
  { value: 'custom', label: 'Custom' },
]

export const HOOP_VALUE_OPTIONS = [
  { value: 'session.id', label: 'Session ID' },
  { value: 'session.user_email', label: 'User email' },
  { value: 'session.user_id', label: 'User ID' },
  { value: 'session.user_name', label: 'User name' },
  { value: 'session.type', label: 'Resource role type' },
  { value: 'session.connection', label: 'Resource role name' },
  { value: 'session.status', label: 'Session status' },
  { value: 'session.start_date', label: 'Session start date' },
  { value: 'session.script', label: 'Session Script' },
]

export const FIELD_TYPE_OPTIONS = [
  { value: 'text', label: 'Text' },
  { value: 'datetime-local', label: 'Date' },
  { value: 'select', label: 'Select' },
]

export const REQUIRED_OPTIONS = [
  { value: 'true', label: 'Yes' },
  { value: 'false', label: 'No' },
]

// Mapping rules whose value points at a resource-role tag are rendered in the
// "resource role tags mapping" table; every other rule (including blank ones)
// belongs to the "automated mapping" table. Both tables share one rows array.
export function isConnectionTagRule(row) {
  return Boolean(row.value?.startsWith(CONNECTION_TAG_PREFIX))
}

export function isNotConnectionTagRule(row) {
  return !isConnectionTagRule(row)
}

export function tagsToSelectOptions(tags) {
  const options = []
  const seen = new Set()
  for (const tag of tags) {
    const value = `${CONNECTION_TAG_PREFIX}${tag.key}`
    if (seen.has(value)) continue
    seen.add(value)
    const parts = tag.key.split('.')
    options.push({ value, label: parts[parts.length - 1] || tag.key })
  }
  return options
}

// Stable unique id for editable table rows (used as React key).
let rowSeq = 0
function nextRowId() {
  rowSeq += 1
  return `row-${rowSeq}`
}

export function createEmptyMappingRow() {
  return {
    id: nextRowId(),
    type: '',
    value: '',
    jira_field: '',
    description: '',
    selected: false,
  }
}

export function createEmptyPromptRow() {
  return {
    id: nextRowId(),
    label: '',
    jira_field: '',
    field_type: '',
    field_options: [],
    required: true,
    description: '',
    selected: false,
  }
}

export function createEmptyCmdbRow() {
  return {
    id: nextRowId(),
    label: '',
    value: '',
    jira_field: '',
    jira_object_schema_id: '',
    jira_object_type: '',
    required: true,
    description: '',
    selected: false,
  }
}

function itemsOf(group) {
  return Array.isArray(group?.items) ? group.items : []
}

// Seed form rows from a JiraIssueTemplate response; every table starts with at
// least one blank row so the user always has something to type into.
export function apiTemplateToMappingRows(template) {
  const rows = itemsOf(template?.mapping_types).map((item) => ({
    ...createEmptyMappingRow(),
    type: item.type ?? '',
    value: item.value ?? '',
    jira_field: item.jira_field ?? '',
    description: item.description ?? '',
  }))
  return rows.length ? rows : [createEmptyMappingRow()]
}

export function apiTemplateToPromptRows(template) {
  const rows = itemsOf(template?.prompt_types).map((item) => ({
    ...createEmptyPromptRow(),
    label: item.label ?? '',
    jira_field: item.jira_field ?? '',
    field_type: item.field_type ?? '',
    field_options: Array.isArray(item.field_options) ? item.field_options : [],
    required: Boolean(item.required),
    description: item.description ?? '',
  }))
  return rows.length ? rows : [createEmptyPromptRow()]
}

export function apiTemplateToCmdbRows(template) {
  const rows = itemsOf(template?.cmdb_types).map((item) => ({
    ...createEmptyCmdbRow(),
    label: item.label ?? '',
    value: item.value ?? '',
    jira_field: item.jira_field ?? '',
    jira_object_schema_id: item.jira_object_schema_id ?? '',
    jira_object_type: item.jira_object_type ?? '',
    required: Boolean(item.required),
    description: item.description ?? '',
  }))
  return rows.length ? rows : [createEmptyCmdbRow()]
}

// Row operations shared by the four rule tables. The two mapping tables pass a
// filterFn because they render disjoint subsets of one shared rows array —
// select/delete must only touch the rows the table actually shows.
export function makeRowOps({ rows, setRows, factory, filterFn = () => true }) {
  const visible = rows.filter(filterFn)
  const allSelected = visible.length > 0 && visible.every((r) => r.selected)
  return {
    visible,
    allSelected,
    patchRow: (id, patch) =>
      setRows((current) =>
        current.map((r) => (r.id === id ? { ...r, ...patch } : r)),
      ),
    toggleSelect: (id) =>
      setRows((current) =>
        current.map((r) => (r.id === id ? { ...r, selected: !r.selected } : r)),
      ),
    toggleAll: () =>
      setRows((current) =>
        current.map((r) =>
          filterFn(r) ? { ...r, selected: !allSelected } : r,
        ),
      ),
    deleteSelected: () =>
      setRows((current) => {
        const remaining = current.filter((r) => !(filterFn(r) && r.selected))
        return remaining.length ? remaining : [factory()]
      }),
    addRow: (transform) =>
      setRows((current) => [
        ...current,
        transform ? transform(factory()) : factory(),
      ]),
  }
}

// Incomplete rows are dropped on submit (same criteria as the legacy app);
// UI-only keys (id, selected) never reach the payload.
function prepareMappingItems(rows) {
  return rows
    .filter((row) => row.type && row.value && row.jira_field)
    .map((row) => ({
      type: row.type,
      value: row.value,
      jira_field: row.jira_field,
      description: row.description,
    }))
}

function preparePromptItems(rows) {
  return rows
    .filter(
      (row) =>
        row.label &&
        row.field_type &&
        row.jira_field &&
        !(row.field_type === 'select' && row.field_options.length === 0),
    )
    .map((row) => ({
      label: row.label,
      jira_field: row.jira_field,
      field_type: row.field_type,
      field_options: row.field_type === 'select' ? row.field_options : [],
      required: row.required,
      description: row.description,
    }))
}

function prepareCmdbItems(rows) {
  return rows
    .filter((row) => row.label && row.jira_field && row.jira_object_type)
    .map((row) => ({
      label: row.label,
      value: row.value,
      jira_field: row.jira_field,
      jira_object_schema_id: row.jira_object_schema_id,
      jira_object_type: row.jira_object_type,
      required: row.required,
      description: row.description,
    }))
}

// Build the JiraIssueTemplateRequest payload from form state.
export function formToPayload(form) {
  return {
    name: form.name.trim(),
    description: form.description,
    project_key: form.projectKey.trim(),
    request_type_id: form.requestTypeId.trim(),
    issue_transition_name_on_close: form.issueTransitionNameOnClose,
    skip_transition_on_nonzero_exit_code: form.skipTransitionOnNonzeroExitCode,
    connection_ids: form.connectionIds,
    mapping_types: { items: prepareMappingItems(form.mappingRows) },
    prompt_types: { items: preparePromptItems(form.promptRows) },
    cmdb_types: { items: prepareCmdbItems(form.cmdbRows) },
  }
}
