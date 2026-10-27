// node --test scripts/dev/seed-resources/seed.test.mjs
//
// The SQL tests run anywhere. The others need a stack: set SEED_DB_URI and
// API_URL.
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { ATTRIBUTES, JOBS, RESOURCES, roles, users } from './catalog.mjs'
import { emptySql, seedSql } from './seed.mjs'
import { psql, str } from '../seed-sessions/seed.mjs'

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

function run(env, ...args) {
  const r = spawnSync(process.execPath, [SEED, ...args], { encoding: 'utf8', maxBuffer: 1 << 26, env })
  assert.equal(r.status, 0, r.stderr)
  return r.stdout
}

function seedAndDump(env) {
  run(env)
  return run(env, 'dump')
}

const stack = process.env.SEED_DB_URI && process.env.API_URL
test('two runs write the same rows', { skip: !stack && 'set SEED_DB_URI and API_URL' }, () => {
  const first = seedAndDump(process.env)
  assert.equal(seedAndDump(process.env), first)
})

async function call(method, path, token, body) {
  const res = await fetch(`${process.env.API_URL.replace(/\/+$/, '')}/api${path}`, {
    method,
    headers: { 'content-type': 'application/json', ...(token && { authorization: `Bearer ${token}` }) },
    body: body && JSON.stringify(body),
  })
  if (!res.ok) assert.fail(`${method} ${path} answered ${res.status}: ${await res.text()}`)
  return res
}

async function admin() {
  const login = await call('POST', '/localauth/login', null, { email: 'admin@seed.example.com', password: 'hoop-seed-1234' })
  const token = login.headers.get('token')
  const agents = await (await call('GET', '/agents', token)).json()
  return { token, agentId: agents.find((a) => a.name === (process.env.SEED_AGENT || 'default')).id }
}

// The flows the matrix records delete, rename and add rows; the next run
// writes the same rows again.
test('a run after the matrix flows writes the same rows', { skip: !stack && 'set SEED_DB_URI and API_URL' }, async () => {
  const first = seedAndDump(process.env)
  const { token, agentId } = await admin()

  await call('DELETE', '/connections/seed-res-many-55', token) // RL20
  await call('DELETE', '/resources/seed-res-no-roles', token) // RC06
  await call('PUT', '/resources/seed-res-python', token, { // RC05
    name: 'seed-res-python-renamed', type: 'custom', subtype: 'python', agent_id: agentId, env_vars: {},
  })
  await call('POST', '/connections', token, { // RA03
    name: 'seed-res-postgres-added', type: 'database', subtype: 'postgres', resource_name: 'seed-res-postgres',
    agent_id: agentId, secret: { 'envvar:HOST': 'eA==' }, access_mode_runbooks: 'enabled', access_mode_exec: 'enabled',
    access_mode_connect: 'enabled', access_schema: 'enabled',
  })

  assert.equal(seedAndDump(process.env), first)
})

const OUTSIDER = 'seed-test-outsider'
const dropOutsider = () => psql(`BEGIN;
  DELETE FROM private.env_vars WHERE id IN (SELECT id FROM private.connections WHERE name = ${str(OUTSIDER)}
    UNION ALL SELECT id FROM private.resources WHERE name = ${str(OUTSIDER)});
  DELETE FROM private.connections WHERE name = ${str(OUTSIDER)};
  DELETE FROM private.resources WHERE name = ${str(OUTSIDER)};
  COMMIT;`)

// A flow can give a seeded attribute to a role the seed does not own.
test('a run keeps a seeded attribute on a role outside the seed', { skip: !stack && 'set SEED_DB_URI and API_URL' }, async (t) => {
  run(process.env)
  const { token, agentId } = await admin()
  t.after(dropOutsider)
  await call('POST', '/connections', token, {
    name: OUTSIDER, type: 'custom', subtype: '', command: ['bash'], agent_id: agentId, attributes: ['seed-prod'],
    access_mode_runbooks: 'enabled', access_mode_exec: 'enabled', access_mode_connect: 'enabled', access_schema: 'disabled',
  })
  run(process.env)
  const conn = await (await call('GET', `/connections/${OUTSIDER}`, token)).json()
  assert.deepEqual(conn.attributes, ['seed-prod'])
})

const OTHER_ORG = '00000000-0000-4000-8000-0000000009a9'
const dropOtherOrg = () => psql(`BEGIN;
  DELETE FROM private.env_vars WHERE org_id = ${str(OTHER_ORG)};
  DELETE FROM private.connections WHERE org_id = ${str(OTHER_ORG)};
  DELETE FROM private.resources WHERE org_id = ${str(OTHER_ORG)};
  DELETE FROM private.users WHERE org_id = ${str(OTHER_ORG)};
  DELETE FROM private.orgs WHERE id = ${str(OTHER_ORG)};
  COMMIT;`)

test('the dump holds the seeded organization only', { skip: !stack && 'set SEED_DB_URI and API_URL' }, (t) => {
  const first = seedAndDump(process.env)
  t.after(dropOtherOrg)
  psql(`BEGIN;
    INSERT INTO private.orgs (id, name) VALUES (${str(OTHER_ORG)}, 'seed-test-other-org');
    INSERT INTO private.resources (org_id, name, type, subtype) VALUES (${str(OTHER_ORG)}, 'other-db', 'database', 'postgres');
    INSERT INTO private.connections (id, org_id, name, resource_name, type, subtype)
      VALUES (${str(OTHER_ORG)}, ${str(OTHER_ORG)}, 'other-db', 'other-db', 'database', 'postgres');
    INSERT INTO private.env_vars (id, org_id, envs) VALUES (${str(OTHER_ORG)}, ${str(OTHER_ORG)}, '{"envvar:PASS": "c2VjcmV0"}');
    INSERT INTO private.users (org_id, subject, email, name, status)
      VALUES (${str(OTHER_ORG)}, 'other-org@seed.example.com', 'other-org@seed.example.com', 'Other org', 'active');
    COMMIT;`)
  assert.equal(run(process.env, 'dump'), first)
})

// Run it on two new databases: the org ids differ, the dumps do not.
const second = stack && process.env.SEED_DB_URI_2 && process.env.API_URL_2
test('two stacks get the same rows', { skip: !second && 'set SEED_DB_URI_2 and API_URL_2 too' }, () => {
  const env2 = { ...process.env, SEED_DB_URI: process.env.SEED_DB_URI_2, API_URL: process.env.API_URL_2 }
  assert.equal(seedAndDump(env2), seedAndDump(process.env))
})
