import { beforeAll, describe, expect, it } from 'vitest'
import submitFixture from './fixtures/jira/jira_submit.json'
import filterFixture from './fixtures/jira/jira_cascade_filter.json'
import dependentsFixture from './fixtures/jira/jira_cascade_dependents.json'
import {
  PENDING,
  allDependents,
  filterFor,
  indexConfigs,
} from '@/features/JiraPromptGate/cascade'
import { initFields, selectCmdb, submitFields } from '@/features/JiraPromptGate/jiraFields'

// The fixtures are the Jira prompt gate vectors with the output of the
// ClojureScript code (npm --prefix ../webapp run fixtures:jira). For the same
// template and answers, the React body must be the CLJS body.

// CLJS keeps the picked name under :selected-name; React under selectedName.
const toRow = ({ 'selected-name': selectedName, ...row }) =>
  selectedName === undefined ? row : { ...row, selectedName }

beforeAll(() => {
  // datetime-local answers are wall-clock times of the generator's zone. Node
  // applies a TZ set at run time.
  globalThis.process.env.TZ = submitFixture.timezone
})

describe('Jira prompt body (CLJS form-data/submit-data)', () => {
  it('runs in the zone the fixtures were written in', () => {
    expect(Intl.DateTimeFormat().resolvedOptions().timeZone).toBe(submitFixture.timezone)
  })

  for (const c of submitFixture.cases) {
    it(c.name, () => {
      const rows = c.cmdb_items
      const configs = indexConfigs(c.fieldconfigs)
      let fields = initFields(rows)
      for (const { kind, jira_field: field, value } of c.events) {
        if (kind === 'prompt') {
          fields = { ...fields, [field]: value }
        } else {
          // The picker hands over the option it shows: the object id and name.
          const row = rows.find((r) => r.jira_field === field)
          const name = row.jira_values.find((v) => v.id === value)?.name
          fields = selectCmdb(fields, row, rows, configs, { value, label: name })
        }
      }
      expect(submitFields(c.prompts, fields)).toEqual(c.expected)
    })
  }
})

describe('CMDB filter (CLJS cascade/filter-for)', () => {
  for (const c of filterFixture.cases) {
    it(c.name, () => {
      const rows = c.items.map(toRow)
      const row = rows.find((r) => r.jira_field === c.item)
      const out = filterFor(row, rows, indexConfigs(c.fieldconfigs))
      const expected =
        c.expected === 'pending'
          ? PENDING
          : c.expected && { aql: c.expected.aql, objectSchemaId: c.expected.object_schema_id }
      expect(out).toEqual(expected)
    })
  }
})

describe('CMDB rows a pick clears (CLJS cascade/all-dependents)', () => {
  for (const c of dependentsFixture.cases) {
    it(c.name, () => {
      const rows = c.items.map(toRow)
      const changed = rows.find((r) => r.jira_field === c.changed)
      const out = allDependents(changed, rows, indexConfigs(c.fieldconfigs))
      expect(out.map((r) => r.jira_field)).toEqual(c.expected)
    })
  }
})
