// Constants and transforms for Live Data Masking rules.
import { ENTITY_TYPES } from '@/pages/sidecarRuleVocabulary'

// Providers whose masking is driven by data-masking rules, so the org can be
// sent straight into the create flow.
export const RULE_DRIVEN_PROVIDERS = ['mspresidio', 'alcatraz']

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

// Every entity the agent's DLP provider or the sidecar's detector names. The
// field accepts anything the operator types besides.
export const ENTITY_OPTIONS = [...new Set([...PRESIDIO_OPTIONS, ...ENTITY_TYPES])].sort()

export const PRESET_OPTIONS = Object.entries(PRESET_DEFINITIONS).map(
  ([value, preset]) => ({ value, label: preset.text }),
)

export function getPresetValues(presetKey) {
  return PRESET_DEFINITIONS[presetKey]?.values ?? []
}

// The preset whose entities are exactly `entities`, for the quick-fill select.
export function presetFor(entities) {
  const set = new Set(entities)
  const match = Object.entries(PRESET_DEFINITIONS).find(
    ([, preset]) => preset.values.length === set.size && preset.values.every((v) => set.has(v)),
  )
  return match ? match[0] : null
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

export function scoreToPercent(scoreThreshold) {
  if (scoreThreshold === null || scoreThreshold === undefined) return ''
  return Math.round(scoreThreshold * 100)
}

// The list badges: the agent's entity groups, else what the sidecar spec names.
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
  if (badges.length === 0) {
    for (const r of rule?.sidecar_spec?.rules ?? []) {
      badges.push(...(r.entities ?? r.columns ?? []))
    }
  }
  return badges
}
