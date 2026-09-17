// Constants and transforms for Live Data Masking rules.

// Preset categories: key -> { text, values[] }
export const PRESET_DEFINITIONS = {
  CONTACT_INFORMATION: {
    text: 'Contact Information',
    values: ['EMAIL_ADDRESS', 'PHONE_NUMBER', 'PERSON'],
  },
  FINANCIAL_DATA: {
    text: 'Financial Data',
    values: ['CREDIT_CARD', 'IBAN_CODE'],
  },
  NETWORK_IDENTIFIERS: {
    text: 'Network Identifiers',
    values: ['IP_ADDRESS', 'URL'],
  },
  LOCATION_DATA: {
    text: 'Location Data',
    values: ['LOCATION'],
  },
  TIME_DATA: {
    text: 'Date & Time Information',
    values: ['DATE_TIME'],
  },
  US_DOCUMENTS: {
    text: 'US Government Documents',
    values: ['US_PASSPORT', 'US_DRIVER_LICENSE', 'US_SSN', 'US_ITIN', 'US_BANK_NUMBER'],
  },
  UK_DOCUMENTS: {
    text: 'UK Government Documents',
    values: ['UK_NHS', 'UK_NINO'],
  },
  EUROPEAN_DOCUMENTS: {
    text: 'European Documents',
    values: [
      'ES_NIF',
      'ES_NIE',
      'IT_FISCAL_CODE',
      'IT_DRIVER_LICENSE',
      'IT_VAT_CODE',
      'IT_PASSPORT',
      'IT_IDENTITY_CARD',
      'PL_PESEL',
      'FI_PERSONAL_IDENTITY_CODE',
    ],
  },
  ASIA_PACIFIC_DOCUMENTS: {
    text: 'Asia Pacific Documents',
    values: [
      'SG_NRIC_FIN',
      'SG_UEN',
      'AU_ABN',
      'AU_ACN',
      'AU_TFN',
      'AU_MEDICARE',
      'IN_PAN',
      'IN_AADHAAR',
      'IN_VEHICLE_REGISTRATION',
      'IN_VOTER',
      'IN_PASSPORT',
    ],
  },
  MEDICAL_DATA: {
    text: 'Medical Information',
    values: ['MEDICAL_LICENSE'],
  },
  DEMOGRAPHIC_DATA: {
    text: 'Demographic Information',
    values: ['NRP'],
  },
  CRYPTO_IDENTIFIERS: {
    text: 'Cryptocurrency Identifiers',
    values: ['CRYPTO'],
  },
}

// DLP entity types available for the "Fields" rule type (presidio-options).
export const PRESIDIO_OPTIONS = [
  'BR_CPF',
  'CREDIT_CARD',
  'CRYPTO',
  'DATE_TIME',
  'EMAIL_ADDRESS',
  'IBAN_CODE',
  'IP_ADDRESS',
  'NRP',
  'LOCATION',
  'ORGANIZATION',
  'PERSON',
  'PHONE_NUMBER',
  'MEDICAL_LICENSE',
  'URL',
  'US_BANK_NUMBER',
  'US_DRIVER_LICENSE',
  'US_ITIN',
  'US_PASSPORT',
  'US_SSN',
  'UK_NHS',
  'UK_NINO',
  'ES_NIF',
  'ES_NIE',
  'IT_FISCAL_CODE',
  'IT_DRIVER_LICENSE',
  'IT_VAT_CODE',
  'IT_PASSPORT',
  'IT_IDENTITY_CARD',
  'PL_PESEL',
  'SG_NRIC_FIN',
  'SG_UEN',
  'AU_ABN',
  'AU_ACN',
  'AU_TFN',
  'AU_MEDICARE',
  'IN_PAN',
  'IN_AADHAAR',
  'IN_VEHICLE_REGISTRATION',
  'IN_VOTER',
  'IN_PASSPORT',
  'FI_PERSONAL_IDENTITY_CODE',
]

export const RULE_TYPES = [
  { value: 'presets', label: 'Presets' },
  { value: 'fields', label: 'Fields' },
]

export const PRESET_OPTIONS = Object.entries(PRESET_DEFINITIONS).map(
  ([value, preset]) => ({ value, label: preset.text }),
)

export const FIELD_OPTIONS = PRESIDIO_OPTIONS.map((v) => ({ value: v, label: v }))

export function getPresetValues(presetKey) {
  return PRESET_DEFINITIONS[presetKey]?.values ?? []
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
    details: '',
    strategy: 'redact',
    columns: [],
    keepLast: 4,
    maskChar: '*',
    selected: false,
  }
}

// UPPERCASE_WITH_UNDERSCORES, stripping anything that isn't [A-Z0-9_].
export function normalizeEntityName(name) {
  if (!name || !name.trim()) return ''
  return name
    .trim()
    .toUpperCase()
    .replace(/\s+/g, '_')
    .replace(/[^A-Z0-9_]/g, '')
}

export function apiRuleToFormRows(rule) {
  const supported = (rule?.supported_entity_types ?? []).map((entity) => {
    const entityValues = entity.entity_types ?? entity.values ?? []
    const type = entity.name === 'CUSTOM_SELECTION' ? 'fields' : 'presets'
    return {
      id: nextRowId(),
      type,
      rule: entity.name,
      details: type === 'fields' ? [...entityValues] : '',
      strategy: entity.strategy ?? 'redact',
      columns: entity.columns ?? [],
      keepLast: entity.keep_last ?? 4,
      maskChar: entity.mask_char ?? '*',
      selected: false,
    }
  })

  const custom = (rule?.custom_entity_types ?? []).map((c) => ({
    id: nextRowId(),
    type: 'custom',
    rule: c.name,
    details: c.regex,
    strategy: c.strategy ?? 'redact',
    columns: c.columns ?? [],
    keepLast: c.keep_last ?? 4,
    maskChar: c.mask_char ?? '*',
    selected: false,
  }))

  const all = [...supported, ...custom]
  return all.length ? all : [createEmptyRow()]
}

function isEmptyRow(row) {
  if (!row.type) return true
  if (row.type === 'fields') {
    return !Array.isArray(row.details) || row.details.length === 0
  }
  return !row.rule
}

function removeEmptyRows(rows) {
  return rows.filter((row) => !isEmptyRow(row))
}

function prepareSupportedEntityTypes(rows) {
  const clean = removeEmptyRows(rows).filter((r) => r.type !== 'custom')

  const result = []
  for (const row of clean) {
    if (row.type === 'fields') {
      const entityTypes = Array.isArray(row.details) ? row.details : []
      result.push({
        name: 'CUSTOM_SELECTION',
        entity_types: entityTypes,
        strategy: row.strategy || 'redact',
        columns: row.columns || [],
        keep_last: row.keepLast ? Number(row.keepLast) : null,
        mask_char: row.maskChar || '*',
      })
    } else {
      result.push({
        name: row.rule,
        entity_types: getPresetValues(row.rule),
        strategy: row.strategy || 'redact',
        columns: row.columns || [],
        keep_last: row.keepLast ? Number(row.keepLast) : null,
        mask_char: row.maskChar || '*',
      })
    }
  }
  return result
}

function prepareCustomEntityTypes(rows) {
  return removeEmptyRows(rows)
    .filter((r) => r.type === 'custom')
    .map((r) => ({
      name: normalizeEntityName(r.rule),
      regex: r.details,
      score: 0.8,
      strategy: r.strategy || 'redact',
      columns: r.columns || [],
      keep_last: r.keepLast ? Number(r.keepLast) : null,
      mask_char: r.maskChar || '*',
    }))
}

// Build the API payload from form state.
export function formToPayload(form) {
  const payload = {
    name: form.name,
    description: form.description,
    connection_ids: form.connectionIds,
    attributes: form.attributes,
    supported_entity_types: prepareSupportedEntityTypes(form.rules),
    custom_entity_types: prepareCustomEntityTypes(form.rules),
    strategy: form.strategy || 'redact',
    columns: form.columns || [],
    keep_last: form.keepLast ? Number(form.keepLast) : null,
    mask_char: form.maskChar || '*',
  }

  const score = form.scoreThreshold
  if (score !== '' && score !== null && score !== undefined) {
    payload.score_threshold = Number(score) / 100
  }
  return payload
}

export function scoreToPercent(scoreThreshold) {
  if (scoreThreshold === null || scoreThreshold === undefined) return ''
  return Math.round(scoreThreshold * 100)
}

export function ruleEntityBadges(rule) {
  const badges = []
  for (const entity of rule?.supported_entity_types ?? []) {
    if (entity.name === 'CUSTOM_SELECTION') {
      badges.push(...(entity.entity_types ?? []))
    } else {
      badges.push(entity.name)
    }
  }
  for (const custom of rule?.custom_entity_types ?? []) {
    badges.push(custom.name)
  }
  return badges
}
