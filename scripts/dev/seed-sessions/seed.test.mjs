// node --test scripts/dev/seed-sessions/seed.test.mjs
//
// The SQL tests run anywhere. "two runs write the same rows" needs a stack:
// set SEED_DB_URI and API_URL (and SEED_LICENSE_FILE on a new database).
// "two stacks get the same rows" also needs SEED_DB_URI_2 and API_URL_2.
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { LEADING, SIDECAR, SIDECAR_EVENTS, SIDECAR_SESSIONS, sessions, uuidv5 } from './catalog.mjs'
import { seedSql, usersSql } from './seed.mjs'
import { users } from './catalog.mjs'

const ORG = '00000000-0000-4000-8000-000000000000'
const SEED = fileURLToPath(new URL('./seed.mjs', import.meta.url))

test('ids match the ones the gateway derives', () => {
  // Values printed by the gateway's own functions: uuid.NewSHA1 for the blob
  // input, models.SessionStreamBlobID and services.SidecarSessionID.
  assert.equal(uuidv5('blobinput:5f7c3c1e-0000-4000-8000-000000000001'), 'e4cc53e5-d394-56f9-9d0e-83372cbd7d79')
  assert.equal(uuidv5('blobstream:5f7c3c1e-0000-4000-8000-000000000001'), '7445d55b-bf6c-5b4c-88fe-3e3345402294')
  assert.equal(SIDECAR.id, '56ff09cd-a17b-5d62-8139-3ccca794f76d')
  assert.equal(SIDECAR_SESSIONS.find((s) => s.key === 'seed-sc-full').id, 'e6191503-a3bf-569c-a19c-83ec8f38d912')
})

test('the SQL does not read the clock', (t) => {
  const build = () => usersSql(ORG, users({ adminGroup: 'admin', auditorGroup: 'auditor' })) + seedSql(ORG)
  t.mock.timers.enable({ apis: ['Date'], now: 0 })
  const first = build()
  t.mock.timers.setTime(Date.parse('2031-01-01T00:00:00Z'))
  assert.equal(build(), first)
})

test('the seed holds every kind the harness needs', () => {
  const list = sessions()
  const keys = new Set(list.map((s) => s.key))
  for (const key of LEADING) assert.ok(keys.has(key), `leading kind ${key}`)
  assert.equal(keys.size, list.length, 'session keys are unique')
  assert.ok(list.length > 100, 'more than 100 sessions')
  assert.ok(list.filter((s) => s.correlation_id === 'seed-workflow-210-steps').length > 200, 'a workflow over 200')
  assert.ok(list.some((s) => s.session_batch_id), 'a batch')
  assert.ok(list.some((s) => s.verb === 'connect' && s.status === 'open'), 'a live session')
  for (const status of ['PENDING', 'APPROVED', 'REJECTED', 'EXECUTED']) {
    assert.ok(list.some((s) => s.review?.status === status), `a ${status} review`)
  }
})

// The kinds the harness ticket lists, one session each.
const TICKET_KINDS = [
  'exec-bash-ok', 'exec-bash-exit-1', 'exec-bash-stderr', 'exec-pg-table', 'exec-pg-error', 'connect-pty',
  'connect-ssh', 'connect-pg-wire', 'connect-http', 'review-pending', 'review-approved', 'review-rejected',
  'review-approved-time-window', 'review-executed', 'connect-live-pty', 'exec-output-5mb', 'exec-input-5mb',
  'workflow-short-3', 'exec-guardrail-block', 'seed-sc-full',
]

test('the first page of /sessions holds every kind the ticket lists', () => {
  // The list sorts by created_at, newest first, 20 rows a page. A sidecar
  // session starts at its session_start event.
  const starts = SIDECAR_EVENTS.filter((e) => e.event.kind === 'session_start')
    .map((e) => ({ key: e.event.session_id, at: e.event.timestamp }))
  const rows = [...sessions().map((s) => ({ key: s.key, at: s.created_at })), ...starts]
    .sort((a, b) => Date.parse(b.at) - Date.parse(a.at))
  const page = rows.slice(0, 20).map((r) => r.key)
  for (const key of TICKET_KINDS) assert.ok(page.includes(key), `${key} is on the first page`)
})

// The rows of the Sessions behavior matrix (project document) whose Data /
// infra column says SEED. README.md must show each one.
const SEED_ROWS = `L01 L02 L03 L04 L05 L06 L07 L08 L09 L10 L11 L12 L14 F01 F02 F03 F04 F07 F08 F09 F10 F11 F12 F13
  F14 B01 B02 B03 B06 D01 D02 D03 D04 D05 D08 D09 H02 H04 FD01 FD03 FD04 FD05 FD06 FD07 FD08 FD09 FD10 FD11 FD12
  FD13 FD15 FD16 FD17 I02 I03 I04 I05 I06 I07 I11 I13 R01 R04 R05 R07 R08 R09 A12 A13 A15 O03 O04 O05 O06 O07 O08
  O09 O10 O12 O13 O14 P01 DL01 DL03 DL04 DL05 W01 W07 W08 W10 W13`.split(/\s+/)

test('README maps every SEED row to sessions that exist', () => {
  const readme = readFileSync(new URL('./README.md', import.meta.url), 'utf8')
  const ids = new Map([...sessions(), ...SIDECAR_SESSIONS].map((s) => [s.key, s.id]))
  const sessionRows = [...readme.matchAll(/^\| `([a-z0-9-]+)` \| `([0-9a-f-]{36})` \|/gm)]
  assert.ok(sessionRows.length > 40, 'the Sessions table')
  for (const [, key, id] of sessionRows) assert.equal(id, ids.get(key), `README id of ${key}`)

  const matrixRows = new Map([...readme.matchAll(/^\| ([A-Z]{1,2}\d\d) \| (.+) \|$/gm)].map((m) => [m[1], m[2]]))
  assert.deepEqual([...matrixRows.keys()], SEED_ROWS)
  for (const [row, shownBy] of matrixRows) {
    for (const [, key] of shownBy.matchAll(/`([a-z][a-z0-9]*(?:-[a-z0-9]+)+)`/g)) {
      if (/^seed-(wf-short|workflow-210-steps|batch-1|reviewers|force|postgres|pg|bash|rdp|k8s|mssql|ssh|sidecar)$/.test(key)) continue
      assert.ok(ids.has(key), `${row} names ${key}, which the catalog does not have`)
    }
  }
})

// Seeds the stack env names, then returns its dump.
function seedAndDump(env) {
  const run = (...args) => {
    const r = spawnSync(process.execPath, [SEED, ...args], { encoding: 'utf8', maxBuffer: 1 << 28, env })
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

// Run it on two new databases: the org ids differ, the dumps do not.
const second = stack && process.env.SEED_DB_URI_2 && process.env.API_URL_2
test('two stacks get the same rows', { skip: !second && 'set SEED_DB_URI_2 and API_URL_2 too' }, () => {
  const env2 = { ...process.env, SEED_DB_URI: process.env.SEED_DB_URI_2, API_URL: process.env.API_URL_2 }
  assert.equal(seedAndDump(env2), seedAndDump(process.env))
})
