import { describe, expect, it } from 'vitest'
import {
  blockedMessage,
  categoriesText,
  infoTypeLabel,
  itemsText,
  maskingAnalytics,
} from '@/features/SessionDetails/insights/helpers'

describe('blockedMessage', () => {
  it('counts forbidden keywords for a deny-words rule', () => {
    expect(blockedMessage('deny_words_list', 'input', ['DELETE'])).toBe('Query blocked — 1 forbidden keyword detected')
    expect(blockedMessage('deny_words_list', 'output', ['a', 'b'])).toBe(
      'Response blocked — 2 forbidden keywords detected',
    )
  })

  it('names a pattern violation and falls back for other rules', () => {
    expect(blockedMessage('pattern_match', 'output')).toBe('Response blocked — pattern violation detected')
    expect(blockedMessage('sidecar', undefined)).toBe('Query blocked')
  })
})

describe('maskingAnalytics', () => {
  const report = {
    items: [
      { info_type: 'PHONE_NUMBER', redact_total: 2 },
      { info_type: 'EMAIL_ADDRESS', redact_total: 2 },
    ],
    total_redact_count: 4,
  }

  it('reads the report when it has data', () => {
    expect(maskingAnalytics(report, {})).toEqual({
      items: [
        { infoType: 'PHONE_NUMBER', count: 2 },
        { infoType: 'EMAIL_ADDRESS', count: 2 },
      ],
      total: 4,
    })
  })

  it('splits the joined info type of a sidecar report (G3)', () => {
    const sidecar = { items: [{ info_type: 'email+ssn', redact_total: 3 }], total_redact_count: 3 }
    expect(maskingAnalytics(sidecar, {})).toEqual({
      items: [
        { infoType: 'email', count: 3 },
        { infoType: 'ssn', count: 3 },
      ],
      total: 3,
    })
  })

  it('falls back to the agent metrics, then to the sidecar metrics', () => {
    const empty = { items: [], total_redact_count: 0 }
    expect(maskingAnalytics(empty, { metrics: { data_analyzer: { PERSON: 2 } } })).toEqual({
      items: [{ infoType: 'PERSON', count: 2 }],
      total: 2,
    })
    expect(maskingAnalytics(null, { metrics: { data_masking: { info_types: { 'email+ssn': 3 } } } })).toEqual({
      items: [
        { infoType: 'email', count: 3 },
        { infoType: 'ssn', count: 3 },
      ],
      total: 3,
    })
    expect(
      maskingAnalytics(null, { metrics: { data_masking: { info_types: { 'email+ssn': 3, email: 2 } } } }),
    ).toEqual({
      items: [
        { infoType: 'email', count: 5 },
        { infoType: 'ssn', count: 3 },
      ],
      total: 5,
    })
    expect(maskingAnalytics(null, {})).toEqual({ items: [], total: 0 })
  })
})

describe('masking texts', () => {
  it('labels info types', () => {
    expect(infoTypeLabel('EMAIL_ADDRESS')).toBe('Email Address')
    expect(infoTypeLabel('date-of-birth')).toBe('Date of Birth')
  })

  it('summarizes categories closed and lists them open', () => {
    const items = [{ infoType: 'PERSON' }, { infoType: 'EMAIL_ADDRESS' }, { infoType: 'PHONE_NUMBER' }]
    expect(categoriesText(items, { full: false })).toBe('3 (Person + 2 more)')
    expect(categoriesText(items.slice(0, 1), { full: false })).toBe('1 (Person)')
    expect(categoriesText(items, { full: true })).toBe('3 (Person, Email Address, Phone Number)')
    expect(categoriesText([], { full: false })).toBe('0')
    expect(categoriesText([], { full: true })).toBe('-')
  })

  it('pluralizes the volume, zero included', () => {
    expect(itemsText(0)).toBe('0 items')
    expect(itemsText(1)).toBe('1 item')
    expect(itemsText(6)).toBe('6 items')
  })
})
