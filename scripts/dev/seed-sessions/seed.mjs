#!/usr/bin/env node
// Seeds a dev stack with every session kind of the Sessions behavior matrix.
// It needs psql and a running gateway with AUTH_METHOD=local and an
// Enterprise license; no Go. See README.md.
//
//   node scripts/dev/seed-sessions/seed.mjs         write the seed (again)
//   node scripts/dev/seed-sessions/seed.mjs dump    print sessions, blobs,
//                                                   reviews and review groups
//                                                   for a diff, without org_id
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import {
  CONNECTIONS, PASSWORD, PASSWORD_HASH, SIDECAR, SIDECAR_EVENTS, SIDECAR_SESSIONS, seedId, sessions, users, uuidv5,
} from './catalog.mjs'

const DB_URI = process.env.SEED_DB_URI || 'postgres://hoopdevuser:1a2b3c4d@127.0.0.1:5449/hoopdevdb?sslmode=disable'
export const API_URL = (process.env.API_URL || 'http://127.0.0.1:8009').replace(/\/+$/, '')
const PSQL = (process.env.PSQL || 'psql').split(/\s+/)
export const ORG_NAME = process.env.SEED_ORG || 'default'
export const ADMIN_GROUP = process.env.ADMIN_USERNAME || 'admin'
const AUDITOR_GROUP = process.env.AUDITOR_USERNAME || 'auditor'
// A license JSON (the license_data of a licensed organization) to upload
// before the sidecar events, for a database that has none yet.
const LICENSE_FILE = process.env.SEED_LICENSE_FILE
const SIDECAR_EVENTS_FLAG = 'experimental.sidecar_session_events'

export function fail(message) {
  console.error(`seed: ${message}`)
  process.exit(1)
}

export function psql(sql) {
  const [cmd, ...args] = PSQL
  const r = spawnSync(cmd, [...args, DB_URI, '-X', '-q', '-At', '-v', 'ON_ERROR_STOP=1', '-f', '-'], {
    input: sql, encoding: 'utf8', maxBuffer: 1 << 30,
  })
  // EPIPE: psql stopped on an error before it read all of its input; its
  // status and stderr tell why.
  if (r.error && r.error.code !== 'EPIPE') fail(`cannot run ${PSQL.join(' ')}: ${r.error.message}`)
  if (r.status !== 0) fail(`psql exited ${r.status}:\n${r.stderr}`)
  return r.stdout
}

// api fails on a status outside 2xx, unless allow lists it.
export async function api(method, path, { token, sidecarToken, body, allow = [] } = {}) {
  const headers = { 'content-type': 'application/json' }
  if (token) headers.authorization = `Bearer ${token}`
  if (sidecarToken) headers['hoop-sidecar-token'] = sidecarToken
  let res
  try {
    res = await fetch(`${API_URL}/api${path}`, { method, headers, body: body && JSON.stringify(body) })
  } catch (err) {
    fail(`${method} ${API_URL}/api${path}: ${err.cause?.message || err.message}. Is the gateway up? Set API_URL.`)
  }
  const text = await res.text()
  if (!res.ok && !allow.includes(res.status)) fail(`${method} /api${path} answered ${res.status}: ${text}`)
  return { res, json: res.ok && text ? JSON.parse(text) : null }
}

// ─── SQL literals ──────────────────────────────────────────────────────────

export const str = (v) => (v === null || v === undefined ? 'NULL' : `'${String(v).replaceAll("'", "''")}'`)
export const num = (v) => (v === null || v === undefined ? 'NULL' : String(Number(v)))
export const json = (v) => (v === null || v === undefined ? 'NULL' : `${str(JSON.stringify(v))}::jsonb`)
export const ts = (v) => (v === null || v === undefined ? 'NULL' : `${str(v)}::timestamptz AT TIME ZONE 'UTC'`)
export const textArray = (v) => (v === null || v === undefined ? 'NULL' : `ARRAY[${v.map(str).join(', ')}]::text[]`)
export const inList = (values) => values.map(str).join(', ')

const blobInputId = (sid) => uuidv5(`blobinput:${sid}`)
const blobStreamId = (sid) => uuidv5(`blobstream:${sid}`)
const reviewInputId = (rid) => uuidv5(`reviewinput:${rid}`)

// ─── Rows ──────────────────────────────────────────────────────────────────

export function usersSql(org, seedUsers) {
  const subjects = Object.values(seedUsers).map((u) => u.email)
  const out = [`DELETE FROM private.user_groups WHERE user_id IN
    (SELECT id FROM private.users WHERE subject IN (${inList(subjects)}));`]
  for (const [key, u] of Object.entries(seedUsers)) {
    out.push(`INSERT INTO private.users (id, org_id, subject, email, name, verified, status, hashed_password, created_at, updated_at)
    VALUES (${str(seedId('user', key))}, ${str(org)}, ${str(u.email)}, ${str(u.email)}, ${str(u.name)}, true, 'active',
      ${str(PASSWORD_HASH)}, ${ts('2026-09-01T00:00:00Z')}, ${ts('2026-09-01T00:00:00Z')})
    ON CONFLICT (subject) DO UPDATE SET org_id = EXCLUDED.org_id, email = EXCLUDED.email, name = EXCLUDED.name,
      verified = true, status = 'active', hashed_password = EXCLUDED.hashed_password;`)
    for (const group of u.groups) {
      out.push(`INSERT INTO private.user_groups (org_id, user_id, name)
      SELECT ${str(org)}, id, ${str(group)} FROM private.users WHERE subject = ${str(u.email)};`)
    }
  }
  return out.join('\n')
}

function connectionsSql(org) {
  return CONNECTIONS.map((c) => `
    INSERT INTO private.resources (id, org_id, name, subtype, type, created_at, updated_at)
    VALUES (${str(seedId('resource', c.resource))}, ${str(org)}, ${str(c.resource)}, ${str(c.subtype)}, ${str(c.type)},
      ${str('2026-09-01T00:00:00Z')}, ${str('2026-09-01T00:00:00Z')})
    ON CONFLICT (org_id, name) DO UPDATE SET subtype = EXCLUDED.subtype, type = EXCLUDED.type;
    INSERT INTO private.connections (id, org_id, name, command, type, subtype, status, resource_name,
      access_mode_runbooks, access_mode_exec, access_mode_connect, access_schema, force_approve_groups, created_at, updated_at)
    VALUES (${str(seedId('connection', c.name))}, ${str(org)}, ${str(c.name)}, ${textArray(c.command ?? null)},
      ${str(c.type)}, ${str(c.subtype)}, 'offline', ${str(c.resource)}, 'enabled', 'enabled', 'enabled', 'disabled',
      ${textArray(c.forceApproveGroups ?? [])}, ${ts('2026-09-01T00:00:00Z')}, ${ts('2026-09-01T00:00:00Z')})
    ON CONFLICT (org_id, name) DO UPDATE SET type = EXCLUDED.type, subtype = EXCLUDED.subtype,
      resource_name = EXCLUDED.resource_name, force_approve_groups = EXCLUDED.force_approve_groups;`).join('\n')
}

export function sidecarSql(org) {
  const keyHash = createHash('sha256').update(SIDECAR.token).digest('hex')
  return `INSERT INTO private.sidecars (id, org_id, name, key_hash, created_by, created_at)
    VALUES (${str(SIDECAR.id)}, ${str(org)}, ${str(SIDECAR.name)}, ${str(keyHash)}, 'admin@seed.example.com',
      ${str('2026-09-01T00:00:00Z')})
    ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, key_hash = EXCLUDED.key_hash;`
}

// The rows the seed owns. Deleting a session also deletes its RDP job,
// detections and masking metrics (ON DELETE CASCADE).
function deleteSql(org, list) {
  const sids = [...list.map((s) => s.id), ...SIDECAR_SESSIONS.map((s) => s.id)]
  const rids = list.filter((s) => s.review).map((s) => s.review.id)
  const blobs = [
    ...sids.flatMap((sid) => [blobInputId(sid), blobStreamId(sid)]),
    ...rids.map(reviewInputId),
  ]
  return `
    DELETE FROM private.reviews WHERE org_id = ${str(org)} AND id IN (${inList(rids)});
    DELETE FROM private.blobs WHERE org_id = ${str(org)} AND id IN (${inList(blobs)});
    DELETE FROM private.sessions WHERE org_id = ${str(org)} AND id IN (${inList(sids)});`
}

function sessionSql(org, s) {
  const user = s.user
  const hasStream = s.stream !== null || s.streamSql
  const out = [`INSERT INTO private.sessions (id, org_id, connection, connection_type, connection_subtype, verb, labels,
      user_id, user_name, user_email, status, blob_input_id, blob_stream_id, metadata, created_at, ended_at, metrics,
      integrations_metadata, exit_code, session_batch_id, ai_analysis, guardrails_info, correlation_id,
      identity_type, origin, recording_format)
    VALUES (${str(s.id)}, ${str(org)}, ${str(s.connection)}, ${str(s.connection_type)}, ${str(s.connection_subtype)},
      ${str(s.verb)}, ${json(s.labels)}, ${str(user.id)}, ${str(user.name)}, ${str(user.email)}, ${str(s.status)},
      ${str(blobInputId(s.id))}, ${hasStream ? str(blobStreamId(s.id)) : 'NULL'}, ${json(s.metadata)},
      ${ts(s.created_at)}, ${ts(s.ended_at)}, ${json(s.metrics)}, ${json(s.integrations_metadata)}, ${num(s.exit_code)},
      ${str(s.session_batch_id)}, ${json(s.ai_analysis)}, ${json(s.guardrails_info)}, ${str(s.correlation_id)},
      ${str(s.identity_type)}, ${str(s.origin)}, ${str(s.recording_format)});`]

  const input = s.inputSql ?? `jsonb_build_array(${str(s.input)}::text)`
  out.push(`INSERT INTO private.blobs (id, org_id, type, blob_stream, created_at)
    VALUES (${str(blobInputId(s.id))}, ${str(org)}, 'session-input', ${input}, ${ts(s.created_at)});`)
  if (hasStream) {
    const stream = s.streamSql ?? json(s.stream)
    out.push(`INSERT INTO private.blobs (id, org_id, type, blob_stream, format, created_at)
      VALUES (${str(blobStreamId(s.id))}, ${str(org)}, 'session-stream', ${stream}, ${str(s.stream_format)},
        ${ts(s.created_at)});`)
  }
  if (s.streamSql) {
    out.push(`UPDATE private.sessions SET metrics = jsonb_set(metrics, '{event_size}',
      to_jsonb((SELECT octet_length(blob_stream::text) FROM private.blobs WHERE id = ${str(blobStreamId(s.id))})))
      WHERE id = ${str(s.id)};`)
  }
  if (s.review) out.push(reviewSql(org, s))
  if (s.rdp_job) {
    const j = s.rdp_job
    out.push(`INSERT INTO private.rdp_analysis_jobs (id, org_id, session_id, status, attempt, last_error, created_at,
      started_at, finished_at)
      VALUES (${str(seedId('rdpjob', s.key))}, ${str(org)}, ${str(s.id)}, ${str(j.status)}, ${num(j.attempt)},
        ${str(j.last_error)}, ${ts(s.ended_at)}, ${ts(s.ended_at)}, ${ts(s.ended_at)});`)
  }
  for (const [i, d] of (s.rdp_detections ?? []).entries()) {
    out.push(`INSERT INTO private.rdp_entity_detections (id, session_id, frame_index, timestamp, entity_type, score,
      x, y, width, height)
      VALUES (${str(seedId('rdpdetection', `${s.key}:${i}`))}, ${str(s.id)}, ${num(d.frame_index)}, ${num(d.timestamp)},
        ${str(d.entity_type)}, ${num(d.score)}, ${num(d.x)}, ${num(d.y)}, ${num(d.width)}, ${num(d.height)});`)
  }
  for (const m of s.session_metrics ?? []) {
    out.push(`INSERT INTO private.session_metrics (id, org_id, session_id, info_type, count_masked, count_analyzed,
      connection_type, connection_subtype, session_created_at, session_ended_at)
      VALUES (${str(seedId('sessionmetric', `${s.key}:${m.info_type}`))}, ${str(org)}, ${str(s.id)},
        ${str(m.info_type)}, ${num(m.count_masked)}, ${num(m.count_analyzed)}, ${str(s.connection_type)},
        ${str(s.connection_subtype)}, ${ts(s.created_at)}, ${ts(s.ended_at)});`)
  }
  return out.join('\n')
}

function reviewSql(org, s) {
  const r = s.review
  const user = s.user
  const hasInput = r.type === 'onetime' && s.input
  const out = [`INSERT INTO private.reviews (id, org_id, session_id, connection_id, connection_name, type, blob_input_id,
      access_duration_sec, status, owner_id, owner_email, owner_name, created_at, revoked_at, time_window,
      access_request_rule_name, min_approvals, force_approval_groups, rejection_reason)
    VALUES (${str(r.id)}, ${str(org)}, ${str(s.id)},
      (SELECT id FROM private.connections WHERE org_id = ${str(org)} AND name = ${str(s.connection)}),
      ${str(s.connection)}, ${str(r.type)}, ${hasInput ? str(reviewInputId(r.id)) : 'NULL'}, ${num(r.access_duration_sec)},
      ${str(r.status)}, ${str(user.id)}, ${str(user.email)}, ${str(user.name)}, ${ts(r.created_at)}, ${ts(r.revoked_at)},
      ${json(r.time_window)}, ${str(r.access_request_rule_name)}, ${num(r.min_approvals)},
      ${textArray(r.force_approval_groups)}, ${str(r.rejection_reason)});`]
  if (hasInput) {
    out.push(`INSERT INTO private.blobs (id, org_id, type, blob_stream, created_at)
      VALUES (${str(reviewInputId(r.id))}, ${str(org)}, 'review-input', jsonb_build_array(${str(s.input)}::text),
        ${ts(r.created_at)});`)
  }
  for (const g of r.groups) {
    const who = g.reviewer
    out.push(`INSERT INTO private.review_groups (id, org_id, review_id, group_name, status, owner_id, owner_email,
        owner_name, reviewed_at)
      VALUES (${str(seedId('reviewgroup', `${s.key}:${g.name}`))}, ${str(org)}, ${str(r.id)}, ${str(g.name)},
        ${str(g.status)}, ${str(who?.id)}, ${str(who?.email)}, ${str(who?.name)}, ${ts(g.reviewed_at)});`)
  }
  return out.join('\n')
}

// The rows of connections, the sidecar and every session, deleted then
// written again.
export function seedSql(org) {
  const list = sessions()
  return ['BEGIN;', connectionsSql(org), sidecarSql(org), deleteSql(org, list), ...list.map((s) => sessionSql(org, s)),
    'COMMIT;'].join('\n')
}

// The gateway stamps now() on a sidecar session's last_event_at, on its two
// blobs and on its stream chunks, and a random id on its masking metrics. Fix
// them to the session's own times and to ids derived from the row.
function sidecarFixSql() {
  const ids = inList(SIDECAR_SESSIONS.map((s) => s.id))
  return `
    UPDATE private.sessions SET metadata = jsonb_set(metadata, '{sidecar,last_event_at}',
      to_jsonb(to_char(ended_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))
      WHERE id IN (${ids});
    UPDATE private.blobs b SET created_at = s.created_at FROM private.sessions s
      WHERE s.id IN (${ids}) AND b.org_id = s.org_id AND b.id IN (s.blob_input_id, s.blob_stream_id);
    UPDATE private.session_stream_chunks c SET created_at = s.created_at FROM private.sessions s
      WHERE s.id IN (${ids}) AND c.org_id = s.org_id AND c.blob_id = s.blob_stream_id;
    UPDATE private.session_metrics SET id = md5('sessionmetric:' || session_id || ':' || info_type)::uuid
      WHERE session_id IN (${ids});`
}

// ─── Commands ──────────────────────────────────────────────────────────────

export function orgId() {
  const id = psql(`SELECT id FROM private.orgs WHERE name = ${str(ORG_NAME)};`).trim()
  if (!id) fail(`no organization named "${ORG_NAME}" in ${DB_URI}. Start the gateway once on this database; it creates it.`)
  return id
}

// The seed admin's token, when API_URL serves the organization of
// SEED_DB_URI; otherwise it stops. The users are the only rows written before
// that check, and only when neither side has them yet.
export async function sameStackLogin(org, seedUsers) {
  const admin = seedUsers.admin.email
  // 401: the gateway has the user with another password; 404: it has none.
  const login = async () => {
    const { res } = await api('POST', '/localauth/login', {
      body: { email: admin, password: PASSWORD }, allow: [401, 404],
    })
    return { token: res.ok ? res.headers.get('token') : null, status: res.status }
  }
  const mismatch = (detail) => fail(`API_URL ${API_URL} and SEED_DB_URI ${dbName()} are not the same stack: ` +
    `${detail}. Point both at one gateway and its database.`)

  let { token, status } = await login()
  if (status === 404 && psql(`SELECT 1 FROM private.users WHERE subject = ${str(admin)};`).trim()) {
    mismatch(`the database has ${admin}, the gateway does not`)
  }
  if (token) await checkOrg(token)
  // Also resets a changed password of a seed user.
  psql(`BEGIN;\n${usersSql(org, seedUsers)}\nCOMMIT;`)
  if (!token) {
    ;({ token, status } = await login())
    if (!token) {
      mismatch(`${admin} was written to the database, and the gateway ` +
        (status === 404 ? 'does not know it' : 'refuses its password'))
    }
    await checkOrg(token)
  }
  return token

  async function checkOrg(t) {
    const { json: me } = await api('GET', '/userinfo', { token: t })
    if (me.org_id !== org) mismatch(`the gateway's organization is ${me.org_id}, the database's is ${org}`)
  }
}

// The database as host and path, without credentials; libpq keyword DSNs
// (host=... dbname=...) are not URLs.
function dbName() {
  try {
    const u = new URL(DB_URI)
    return `${u.host}${u.pathname}`
  } catch {
    return '(a keyword DSN)'
  }
}

async function seed() {
  const { json: info } = await api('GET', '/publicserverinfo')
  if (info.auth_method !== 'local') {
    fail(`the gateway uses AUTH_METHOD=${info.auth_method}; the seed users log in with a local password. Run it with AUTH_METHOD=local.`)
  }
  const org = orgId()
  const seedUsers = users({ adminGroup: ADMIN_GROUP, auditorGroup: AUDITOR_GROUP })
  const token = await sameStackLogin(org, seedUsers)
  if (LICENSE_FILE) {
    await api('PUT', '/orgs/license', { token, body: JSON.parse(readFileSync(LICENSE_FILE, 'utf8')) })
  }
  const { json: server } = await api('GET', '/serverinfo', { token })
  const license = server.license_info ?? {}
  if (license.type !== 'enterprise' || !license.is_valid) {
    fail(`the organization has no valid Enterprise license (type=${license.type}, status=${license.status}` +
      `${license.verify_error ? `, ${license.verify_error}` : ''}); POST /api/sidecars/events needs one.` +
      ' Set SEED_LICENSE_FILE to a license JSON and run again. See README.md.')
  }

  psql(seedSql(org))

  await api('PUT', `/feature-flags/${SIDECAR_EVENTS_FLAG}`, { token, body: { enabled: true } })
  const { json: accepted } = await api('POST', '/sidecars/events', {
    sidecarToken: SIDECAR.token, body: { events: SIDECAR_EVENTS },
  })
  if (accepted.accepted !== SIDECAR_EVENTS.length) {
    fail(`POST /api/sidecars/events accepted ${accepted.accepted} of ${SIDECAR_EVENTS.length} events: ${JSON.stringify(accepted)}`)
  }
  psql(`BEGIN;\n${sidecarFixSql()}\nCOMMIT;`)

  const count = psql(`SELECT count(*) FROM private.sessions WHERE org_id = ${str(org)} AND id IN
    (${inList([...sessions().map((s) => s.id), ...SIDECAR_SESSIONS.map((s) => s.id)])});`).trim()
  console.log(`seed: ${count} sessions in organization ${ORG_NAME}, ${Object.keys(seedUsers).length} users ` +
    `(password ${PASSWORD}), ${CONNECTIONS.length} connections. Open ${API_URL}/sessions.`)
}

// One JSON object per row, ordered by id, without org_id: it is random per
// database. A stream chunk also drops its id, a sequence. Blob content is replaced by its md5 to keep the dump small.
function dump() {
  process.stdout.write(psql(`
    SELECT '-- private.sessions';
    SELECT to_jsonb(s) - 'org_id' FROM private.sessions s ORDER BY s.id;
    SELECT '-- private.blobs';
    SELECT (to_jsonb(b) - 'org_id' - 'blob_stream') || jsonb_build_object('blob_stream_md5', md5(b.blob_stream::text))
      FROM private.blobs b ORDER BY b.id;
    SELECT '-- private.reviews';
    SELECT to_jsonb(r) - 'org_id' FROM private.reviews r ORDER BY r.id;
    SELECT '-- private.review_groups';
    SELECT to_jsonb(g) - 'org_id' FROM private.review_groups g ORDER BY g.id;
    SELECT '-- private.session_metrics';
    SELECT to_jsonb(m) - 'org_id' FROM private.session_metrics m ORDER BY m.id;
    SELECT '-- private.session_stream_chunks';
    SELECT to_jsonb(c) - 'org_id' - 'id' FROM private.session_stream_chunks c ORDER BY c.blob_id, c.id;`))
}

// Run as a command; seed.test.mjs imports the SQL builders.
if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const command = process.argv[2] ?? 'seed'
  if (command === 'seed') await seed()
  else if (command === 'dump') dump()
  else fail(`unknown command "${command}"; use seed or dump`)
}
