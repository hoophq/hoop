// The two rule engines the gateway understands.
export const RULE_TYPE_OPTIONS = [
  { value: 'deny_words_list', label: 'Deny Word' },
  { value: 'pattern_match', label: 'Pattern Match' },
]

// Sentinel for the "write your own" entry of the Rule column.
export const CUSTOM_RULE = 'custom-rule'

// Ready-made rules offered in the Rule column, grouped by the type they apply
// to. Picking one fills in the configuration so admins don't have to write the
// regex or word list themselves.
export const PRESETS = {
  'require-where-delete': {
    label: 'Require WHERE clause (DELETE)',
    type: 'pattern_match',
    pattern_regex: '(?i)DELETE\\s+FROM\\s+(\\w+\\.)*\\w+[^WHERE]*$',
  },
  'block-password': {
    label: 'Block Passwords',
    type: 'deny_words_list',
    words: ['password', 'senha', 'pass', 'pwd'],
  },
}

export function presetOptionsForType(type) {
  return Object.entries(PRESETS)
    .filter(([, preset]) => preset.type === type)
    .map(([value, preset]) => ({ value, label: preset.label }))
}

// Stable unique id for editable table rows (used as React key).
let rowSeq = 0
function nextRowId() {
  rowSeq += 1
  return `row-${rowSeq}`
}

export function createEmptyRow() {
  return {
    id: nextRowId(),
    type: '',
    rule: '',
    pattern_regex: '',
    words: [],
    message: '',
    selected: false,
  }
}

function sameWords(a, b) {
  if (a.length !== b.length) return false
  const left = [...a].sort()
  const right = [...b].sort()
  return left.every((word, index) => word === right[index])
}

// Which Rule-column entry a stored rule maps back to when reopening a guardrail.
//
// The ClojureScript original looked presets up by the stringified word list
// ["password"], but block-password stores four words, so every saved preset
// came back as a custom rule. Comparing against the preset definition keeps
// presets round-tripping through save/edit.
function identifyPreset(type, patternRegex, words) {
  if (type === 'pattern_match' && patternRegex) {
    const match = Object.entries(PRESETS).find(
      ([, preset]) => preset.pattern_regex === patternRegex
    )
    return match ? match[0] : CUSTOM_RULE
  }
  if (type === 'deny_words_list' && words?.length) {
    const match = Object.entries(PRESETS).find(
      ([, preset]) => preset.words && sameWords(preset.words, words)
    )
    return match ? match[0] : CUSTOM_RULE
  }
  return type ? CUSTOM_RULE : ''
}

// An API section ({ rules: [...] }) to editable rows. Always yields at least
// one row so each table opens with a blank line ready to fill in.
export function apiRulesToRows(section) {
  const rules = section?.rules ?? []
  if (!rules.length) return [createEmptyRow()]

  return rules.map((rule) => {
    const words = [...(rule.words ?? [])]
    const patternRegex = rule.pattern_regex ?? ''
    return {
      id: nextRowId(),
      type: rule.type ?? '',
      rule: identifyPreset(rule.type, patternRegex, words),
      pattern_regex: patternRegex,
      words,
      message: rule.message ?? '',
      selected: false,
    }
  })
}

// A row carries no enforceable configuration: it is dropped on save.
function isEmptyRule(row) {
  if (!row.type) return true
  return !row.words?.length && !row.pattern_regex
}

// Dropping empty rows would silently discard a message the admin typed, so
// saving is blocked until those rows are configured or cleared. Returns the
// error text to show, or null when there is nothing to report.
export function orphanMessageError(inputRows, outputRows) {
  const orphans = [...inputRows, ...outputRows].filter(
    (row) => isEmptyRule(row) && row.message?.trim()
  )
  if (!orphans.length) return null
  if (orphans.length === 1) {
    return 'A rule has a custom error message but no configured words or pattern. Configure the rule or clear its message before saving.'
  }
  return `${orphans.length} rules have a custom error message but no configured words or pattern. Configure the rules or clear their messages before saving.`
}

function rowsToSection(rows) {
  return {
    rules: rows.filter((row) => !isEmptyRule(row)).map((row) => ({
      type: row.type,
      words: row.words ?? [],
      pattern_regex: row.pattern_regex ?? '',
      message: row.message ?? '',
    })),
  }
}

// Build the API payload from form state.
export function formToPayload(form) {
  return {
    id: form.id ?? '',
    name: form.name,
    description: form.description,
    connection_ids: form.connectionIds,
    attributes: form.attributes,
    input: rowsToSection(form.inputRules),
    output: rowsToSection(form.outputRules),
  }
}
