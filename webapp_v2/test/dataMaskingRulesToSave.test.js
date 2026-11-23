import { describe, expect, it } from 'vitest'
import { rulesToSave } from '@/pages/Features/DataMasking/helpers'

const custom = [{ key: 'c1', name: 'RG_BR', regex: '^\\d{7,10}' }]
const rule = (over) => ({ name: '', match: 'entities', entities: [], columns: [], strategy: 'redact', ...over })

describe('rulesToSave', () => {
  // ENG-1241: a rule made with custom entity types only opens with one blank row.
  it('saves custom entity types with a blank rule row', () => {
    expect(rulesToSave([rule()], custom)).toEqual({ rules: [] })
  })

  it('refuses a named rule with no entity types', () => {
    expect(rulesToSave([rule({ name: 'custom' })], custom)).toEqual({
      error: 'Rule 1 needs an entity type, or remove it.',
    })
  })

  it('refuses a named column rule with no columns, numbered as the form shows it', () => {
    const rules = [rule(), rule({ name: 'cols', match: 'columns' })]
    expect(rulesToSave(rules, custom)).toEqual({ error: 'Rule 2 needs a column, or remove it.' })
  })

  it('still asks for a rule name when there are no custom entity types', () => {
    expect(rulesToSave([rule()], [])).toEqual({ error: 'Give every rule a name, or remove it.' })
    expect(rulesToSave([rule()], [{ key: 'c', name: 'RG_BR', regex: '' }])).toEqual({
      error: 'Give every rule a name, or remove it.',
    })
  })

  it('keeps named rules with entity types, as before', () => {
    const named = rule({ name: 'pii', entities: ['EMAIL_ADDRESS'] })
    expect(rulesToSave([named, rule()], custom)).toEqual({ rules: [named] })
    expect(rulesToSave([named], [])).toEqual({ rules: [named] })
  })
})
