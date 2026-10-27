// node --test scripts/dev/seed-resources/seed.test.mjs
//
// The SQL tests run anywhere. "two runs write the same rows" needs a stack:
// set SEED_DB_URI and API_URL.
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { ATTRIBUTES, JOBS, RESOURCES, roles, users } from './catalog.mjs'
import { emptySql, seedSql } from './seed.mjs'

const ORG = '00000000-0000-4000-8000-000000000000'
const AGENT = '75122bce-f957-49eb-a812-2ab60977cd9f'
const SEED = fileURLToPath(new URL('./seed.mjs', import.meta.url))

test('the SQL does not read the clock', (t) => {
  const build = () => seedSql(ORG, AGENT) + emptySql(ORG)
  t.mock.timers.enable({ apis: ['Date'], now: 0 })
  const first = build()
  t.mock.timers.setTime(Date.parse('2031-01-01T00:00:00Z'))
  assert.equal(build(), first)
})

// The families of the inventory §4 (project document "Resources behavior
// inventory").
const FAMILIES = ['Metadata-driven database', 'Dev env', 'SSH', 'TCP', 'Linux VM', 'HTTP proxy', 'Claude Code', 'MCP',
  'MCP Gateway', 'BigQuery with federation']

test('the seed holds what ENG-909 lists', () => {
  const families = RESOURCES.filter((r) => r.family)
  assert.deepEqual(families.map((r) => r.family), FAMILIES)
  for (const r of families) {
    assert.ok(r.roles.length >= 1 && r.roles.length <= 3, `${r.name} has 1 to 3 roles`)
    assert.notEqual(r.agent, false, `${r.name} runs on the agent`)
  }
  const all = roles()
  assert.equal(new Set(all.map((r) => r.name)).size, all.length, 'role names are unique')
  assert.equal(new Set(RESOURCES.map((r) => r.name)).size, RESOURCES.length, 'resource names are unique')
  assert.ok(RESOURCES.some((r) => r.roles.length > 50), 'a resource with more than 50 roles')
  assert.ok(all.some((r) => r.managedBy === 'sidecar' && r.sidecar), 'a sidecar mirror')
  assert.ok(all.some((r) => r.managedBy === 'hoopagent'), 'a role managed by hoopagent')
  assert.ok(all.some((r) => Object.keys(r.tags ?? {}).length && r.attributes?.length), 'a role with tags and attributes')
  assert.ok(all.some((r) => r.subtype === 'bigquery' && r.federation), 'a BigQuery role with a federation config')
  for (const r of all) {
    for (const a of r.attributes ?? []) assert.ok(ATTRIBUTES.some((x) => x.name === a), `${r.name}: attribute ${a}`)
  }
  assert.deepEqual(JOBS.map((j) => j.status.phase).sort(), ['completed', 'failed', 'running'])
  assert.deepEqual(Object.keys(users({ adminGroup: 'admin' })), ['admin', 'standard'])
})

// The rows of the Resources behavior matrix (project document) whose Data
// column says SEED. README.md must map each one.
const SEED_ROWS = `RL01 RL02 RL03 RL04 RL05 RL06 RL07 RL08 RL09 RL10 RL11 RL12 RL13 RL14 RL15 RL16 RL17 RL19 RL20 RL21
  RL22 RL23 RL24 RL25 RC01 RC02 RC03 RC04 RC05 RC06 RC07 RC08 RC09 RC10 RA01 RA02 RA03 RA04 RA05 RA06 RA07 RA08 RA09
  RW01 RW02 RW03 RW04 RW05 RW06 RW07 RW08 RW09 RW11 RW12 RW13 RW14 RW15 RW16 RW18 RW19 RF01 RF02 RF03 RF04 RF05 RF06
  RF07 RF09 RF10 CA01 CA02 CA03 CA04 CA05 ON01 AW01 AW02 AW03 AW04 AW12 LK01 LK03 LK04 LK06 LK07 Q1 Q2 Q3 Q4 Q5 Q6 Q7 Q8 Q9 Q10
  Q11 Q12 Q13 Q14 Q15 Q16 Q17 Q18 Q19 Q20 Q21 Q22 Q23 Q28 Q33 Q34`.split(/\s+/)

test('README maps every SEED row to names the seed has', () => {
  const readme = readFileSync(new URL('./README.md', import.meta.url), 'utf8')
  const rows = new Map([...readme.matchAll(/^\| ((?:[A-Z]{2}\d\d)|(?:Q\d+)) \| (.+) \|$/gm)].map((m) => [m[1], m[2]]))
  assert.deepEqual([...rows.keys()], SEED_ROWS)

  const u = users({ adminGroup: 'admin' })
  const names = new Set([
    ...RESOURCES.map((r) => r.name), ...roles().map((r) => r.name), ...ATTRIBUTES.map((a) => a.name),
    ...JOBS.map((j) => j.key), ...Object.values(u).map((x) => x.email),
  ])
  for (const [row, shownBy] of rows) {
    for (const [, name] of shownBy.matchAll(/`([^`]+)`/g)) {
      // A seeded name, not a path, a query or a missing resource on purpose.
      if (!/^(seed-|[a-z]+@seed\.|running$|completed$|failed$)/.test(name) || name === 'seed-res-missing') continue
      assert.ok(names.has(name), `${row} names ${name}, which the seed does not have`)
    }
  }
})

function seedAndDump(env) {
  const run = (...args) => {
    const r = spawnSync(process.execPath, [SEED, ...args], { encoding: 'utf8', maxBuffer: 1 << 26, env })
    assert.equal(r.status, 0, r.stderr)
    return r.stdout
  }
  run()
  return run('dump')
}

const stack = process.env.SEED_DB_URI && process.env.API_URL
test('two runs write the same rows', { skip: !stack && 'set SEED_DB_URI and API_URL' }, () => {
  const first = seedAndDump(process.env)
  assert.equal(seedAndDump(process.env), first)
})
