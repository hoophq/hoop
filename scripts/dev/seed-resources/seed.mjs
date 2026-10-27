#!/usr/bin/env node
// Seeds a dev stack with every resource family of the Resources behavior
// matrix and the AWS Connect jobs. It needs psql and a running gateway with
// AUTH_METHOD=local; no Go. See README.md.
//
//   node scripts/dev/seed-resources/seed.mjs         write the seed (again)
//   node scripts/dev/seed-resources/seed.mjs empty   delete every connection and
//                                                    resource of the organization
//                                                    and its protection profile
//   node scripts/dev/seed-resources/seed.mjs dump    print the tables the seed
//                                                    writes, for a diff
import { pathToFileURL } from 'node:url'
import {
  ADMIN_GROUP, API_URL, ORG_NAME, api, fail, inList, json, orgId, psql, sameStackLogin, sidecarSql, str, textArray, ts,
} from '../seed-sessions/seed.mjs'
import { seedId } from '../seed-sessions/catalog.mjs'
import { AGENTS_FLAG, ATTRIBUTES, AT, JOBS, RESOURCES, SIDECAR, roles, users } from './catalog.mjs'

const AGENT_NAME = process.env.SEED_AGENT || 'default'

const b64 = (v) => Buffer.from(v).toString('base64')
const envsOf = (envs) => Object.fromEntries(Object.entries(envs ?? {}).map(([k, v]) => [k, b64(v)]))

// ─── Rows ──────────────────────────────────────────────────────────────────

// The seeded rows by name or by id, since a flow can rename them, and the
// roles a flow added to a seeded resource: they hold it
// (connection_resource_fk). The DELETE routes leave env_vars rows behind,
// so those go by their seeded ids too.
function deleteSql(org) {
  const roleIds = roles().map((r) => seedId('connection', r.name))
  const resourceIds = RESOURCES.map((r) => seedId('resource', r.name))
  const resources = `org_id = ${str(org)} AND (name IN (${inList(RESOURCES.map((r) => r.name))})
    OR id IN (${inList(resourceIds)}))`
  const conns = `org_id = ${str(org)} AND (name IN (${inList(roles().map((r) => r.name))})
    OR id IN (${inList(roleIds)}) OR resource_name IN (SELECT name FROM private.resources WHERE ${resources}))`
  return `
    DELETE FROM private.env_vars WHERE org_id = ${str(org)} AND (id IN (${inList([...roleIds, ...resourceIds])})
      OR id IN (SELECT id FROM private.connections WHERE ${conns})
      OR id IN (SELECT id FROM private.resources WHERE ${resources}));
    DELETE FROM private.connections WHERE ${conns};
    DELETE FROM private.resources WHERE ${resources};
    DELETE FROM private.attributes WHERE org_id = ${str(org)} AND (name IN (${inList(ATTRIBUTES.map((a) => a.name))})
      OR id IN (${inList(ATTRIBUTES.map((a) => seedId('attribute', a.name)))}));
    DELETE FROM private.dbrole_jobs WHERE org_id = ${str(org)} AND id IN (${inList(JOBS.map((j) => j.id))});`
}

// The status a connection of this agent gets now: the gateway sets it from
// the agent's stream, and again each time the agent connects.
const statusOf = (agentId) => agentId
  ? `(SELECT CASE WHEN status = 'CONNECTED' THEN 'online' ELSE 'offline' END
      FROM private.agents WHERE id = ${str(agentId)})::private.enum_connection_status`
  : `'offline'`

function resourceSql(org, agentId, r) {
  const agent = r.agent === false ? null : agentId
  return `INSERT INTO private.resources (id, org_id, name, type, subtype, agent_id, created_at, updated_at)
    VALUES (${str(seedId('resource', r.name))}, ${str(org)}, ${str(r.name)}, ${str(r.type)}, ${str(r.subtype)},
      ${str(agent)}, ${str(AT)}, ${str(AT)});
    INSERT INTO private.env_vars (id, org_id, envs, updated_at)
    VALUES (${str(seedId('resource', r.name))}, ${str(org)}, '{}'::jsonb, ${ts(AT)});`
}

function roleSql(org, agentId, r) {
  const id = seedId('connection', r.name)
  const agent = r.sidecar ? null : agentId
  const out = [`INSERT INTO private.connections (id, org_id, name, resource_name, agent_id, command, type, subtype,
      status, managed_by, sidecar_id, sidecar_listener, access_mode_runbooks, access_mode_exec, access_mode_connect,
      access_schema, secrets_updated_at, created_at, updated_at)
    VALUES (${str(id)}, ${str(org)}, ${str(r.name)}, ${str(r.resource)}, ${str(agent)}, ${textArray(r.command ?? null)},
      ${str(r.type)}, ${str(r.subtype)}, ${statusOf(agent)}, ${str(r.managedBy)}, ${str(r.sidecar ? SIDECAR.id : null)},
      ${str(r.sidecar?.listener)}, ${str(r.access)}, ${str(r.access)}, ${str(r.access)}, ${str(r.accessSchema)},
      ${Object.keys(r.envs ?? {}).length ? str(AT) : 'NULL'}, ${ts(AT)}, ${ts(AT)});
    INSERT INTO private.env_vars (id, org_id, envs, updated_at)
    VALUES (${str(id)}, ${str(org)}, ${json(envsOf(r.envs))}, ${ts(AT)});`]
  for (const [key, value] of Object.entries(r.tags ?? {})) {
    out.push(`INSERT INTO private.connection_tags (id, org_id, key, value, created_at, updated_at)
      VALUES (${str(seedId('tag', `${key}=${value}`))}, ${str(org)}, ${str(key)}, ${str(value)}, ${ts(AT)}, ${ts(AT)})
      ON CONFLICT DO NOTHING;
    INSERT INTO private.connection_tags_association (id, org_id, tag_id, connection_id, created_at)
      SELECT ${str(seedId('tagassoc', `${r.name}:${key}`))}, ${str(org)}, t.id, ${str(id)}, ${ts(AT)}
      FROM private.connection_tags t WHERE t.org_id = ${str(org)} AND t.key = ${str(key)} AND t.value = ${str(value)};`)
  }
  for (const name of r.attributes ?? []) {
    out.push(`INSERT INTO private.connections_attributes (org_id, connection_name, attribute_name)
      VALUES (${str(org)}, ${str(r.name)}, ${str(name)});`)
  }
  if (r.federation) {
    const f = r.federation
    out.push(`INSERT INTO private.connection_federation_configs (id, org_id, connection_id, hook_source, builtin_provider,
        admin_credentials_encrypted, identity_source_attribute, identity_target_template, fallback_policy,
        token_ttl_seconds, extra_config, created_at, updated_at)
      VALUES (${str(seedId('federation', r.name))}, ${str(org)}, ${str(id)}, 'builtin', ${str(f.builtin_provider)}, NULL,
        ${str(f.identity_source_attribute)}, ${str(f.identity_target_template)}, ${str(f.fallback_policy)},
        ${f.token_ttl_seconds}, ${json(f.extra_config)}, ${str(AT)}, ${str(AT)});`)
  }
  return out.join('\n')
}

function attributeSql(org, a) {
  return `INSERT INTO private.attributes (id, org_id, name, description, created_at)
    VALUES (${str(seedId('attribute', a.name))}, ${str(org)}, ${str(a.name)}, ${str(a.description)}, ${ts(AT)});`
}

function jobSql(org, j) {
  return `INSERT INTO private.dbrole_jobs (id, org_id, spec, status, hook_status, created_at, completed_at)
    VALUES (${str(j.id)}, ${str(org)}, ${json(j.spec)}, ${str(JSON.stringify(j.status))}::json,
      ${j.hook_status ? `${str(JSON.stringify(j.hook_status))}::json` : 'NULL'}, ${ts(j.created_at)}, ${ts(j.completed_at)});`
}

// The rows of the seed, deleted then written again. agentId is the stack's
// agent every role but the sidecar mirror runs on.
export function seedSql(org, agentId) {
  return ['BEGIN;', deleteSql(org), sidecarSql(org),
    ...ATTRIBUTES.map((a) => attributeSql(org, a)),
    ...RESOURCES.map((r) => resourceSql(org, agentId, r)),
    ...roles().map((r) => roleSql(org, agentId, r)),
    ...JOBS.map((j) => jobSql(org, j)),
    'COMMIT;'].join('\n')
}

// Every connection and resource of the organization. An event subscription
// holds its connection (ON DELETE RESTRICT): the transaction then fails, and
// nothing is deleted.
export function emptySql(org) {
  return `BEGIN;
    DELETE FROM private.env_vars WHERE org_id = ${str(org)} AND id IN (
      SELECT id FROM private.connections WHERE org_id = ${str(org)}
      UNION ALL SELECT id FROM private.resources WHERE org_id = ${str(org)});
    DELETE FROM private.connections WHERE org_id = ${str(org)};
    DELETE FROM private.resources WHERE org_id = ${str(org)};
    COMMIT;`
}

// ─── Commands ──────────────────────────────────────────────────────────────

function agentId(org) {
  const id = psql(`SELECT id FROM private.agents WHERE org_id = ${str(org)} AND name = ${str(AGENT_NAME)};`).trim()
  if (!id) fail(`no agent named "${AGENT_NAME}" in organization ${ORG_NAME}. make run-dev starts one; or set SEED_AGENT.`)
  return id
}

async function login() {
  const { json: info } = await api('GET', '/publicserverinfo')
  if (info.auth_method !== 'local') {
    fail(`the gateway uses AUTH_METHOD=${info.auth_method}; the seed users log in with a local password. Run it with AUTH_METHOD=local.`)
  }
  const org = orgId()
  const token = await sameStackLogin(org, users({ adminGroup: ADMIN_GROUP }))
  await api('PUT', `/feature-flags/${AGENTS_FLAG}`, { token, body: { enabled: true } })
  return { org, token }
}

// Reads every seeded row back through the routes the pages call, so a
// column the gateway reads differently fails here and not in a recording.
async function check(token) {
  for (const r of RESOURCES.filter((r) => !r.hidden)) {
    const { json: page } = await api('GET', `/connections?resource_name=${r.name}&page_size=100&page=1`, { token })
    const want = r.roles.map((x) => x.name).sort()
    const got = page.data.map((c) => c.name).sort()
    if (JSON.stringify(got) !== JSON.stringify(want)) fail(`GET /connections?resource_name=${r.name} lists ${got}, want ${want}`)
  }
  const { json: resources } = await api('GET', '/resources?search=seed-&page_size=100&page=1', { token })
  const listed = new Set(resources.data.map((r) => r.name))
  for (const r of RESOURCES) {
    if (listed.has(r.name) === Boolean(r.hidden)) fail(`GET /resources ${r.hidden ? 'lists' : 'does not list'} ${r.name}`)
  }
  for (const r of roles().filter((r) => r.federation)) {
    await api('GET', `/connections/${r.name}/federation`, { token })
  }
  const { json: jobs } = await api('GET', '/dbroles/jobs', { token })
  const jobIds = new Set(jobs.items.map((j) => j.id))
  for (const j of JOBS) if (!jobIds.has(j.id)) fail(`GET /dbroles/jobs does not list job ${j.key} (${j.id})`)
}

async function seed() {
  const { org, token } = await login()
  psql(seedSql(org, agentId(org)))
  // The gateway encrypts the admin credentials with its own key; the PUT
  // keeps the row's id and created_at and stamps updated_at, set back after.
  const federated = roles().filter((r) => r.federation)
  for (const r of federated) {
    await api('PUT', `/connections/${r.name}/federation`, { token, body: r.federation })
  }
  psql(`UPDATE private.connection_federation_configs SET updated_at = ${str(AT)}
    WHERE org_id = ${str(org)} AND id IN (${inList(federated.map((r) => seedId('federation', r.name)))});`)
  await check(token)
  console.log(`seed: ${RESOURCES.length} resources, ${roles().length} roles, ${JOBS.length} AWS Connect jobs in ` +
    `organization ${ORG_NAME}. Users: see README.md. Open ${API_URL}/resources.`)
}

async function empty() {
  const { org, token } = await login()
  psql(emptySql(org))
  // null also removes the profile's managed rules and attribute.
  await api('PUT', '/orgs/protection-profile', { token, body: { profile: null, source: 'settings' } })
  const { json: left } = await api('GET', '/connections', { token })
  if (left.length) fail(`GET /connections still lists ${left.map((c) => c.name)}`)
  const { json: profile } = await api('GET', '/orgs/protection-profile', { token })
  if (profile.profile) fail(`GET /orgs/protection-profile still answers ${profile.profile}`)
  console.log(`seed: organization ${ORG_NAME} has no connection, no resource and no protection profile. ` +
    `Open ${API_URL}/onboarding.`)
}

// One JSON object per row, ordered by a key, without org_id: it is random per
// database. An agent shows as its name: run-dev fixes its id, other stacks do not.
function dump() {
  const agent = (alias) => `jsonb_build_object('agent', (SELECT name FROM private.agents a WHERE a.id = ${alias}.agent_id))`
  const tagIds = roles().flatMap((r) => Object.entries(r.tags ?? {}).map(([k, v]) => seedId('tag', `${k}=${v}`)))
  process.stdout.write(psql(`
    SELECT '-- private.resources';
    SELECT (to_jsonb(r) - 'org_id' - 'agent_id') || ${agent('r')} FROM private.resources r ORDER BY r.name;
    SELECT '-- private.connections';
    SELECT (to_jsonb(c) - 'org_id' - 'agent_id') || ${agent('c')} FROM private.connections c ORDER BY c.name;
    SELECT '-- private.env_vars';
    SELECT to_jsonb(e) - 'org_id' FROM private.env_vars e ORDER BY e.id;
    SELECT '-- private.connection_tags (the seed''s: the gateway writes its defaults at boot)';
    SELECT to_jsonb(t) - 'org_id' FROM private.connection_tags t WHERE t.id IN (${inList(tagIds)}) ORDER BY t.id;
    SELECT '-- private.connection_tags_association';
    SELECT to_jsonb(t) - 'org_id' FROM private.connection_tags_association t ORDER BY t.id;
    SELECT '-- private.attributes (the seed''s: the gateway writes the rulepack ones at boot)';
    SELECT to_jsonb(a) - 'org_id' FROM private.attributes a WHERE a.name IN (${inList(ATTRIBUTES.map((a) => a.name))})
      ORDER BY a.name;
    SELECT '-- private.connections_attributes';
    SELECT to_jsonb(a) - 'org_id' FROM private.connections_attributes a ORDER BY a.connection_name, a.attribute_name;
    SELECT '-- private.connection_federation_configs (whether admin credentials are set: the nonce is random)';
    SELECT (to_jsonb(f) - 'org_id' - 'admin_credentials_encrypted')
      || jsonb_build_object('has_admin_credentials', f.admin_credentials_encrypted IS NOT NULL)
      FROM private.connection_federation_configs f ORDER BY f.id;
    SELECT '-- private.dbrole_jobs';
    SELECT to_jsonb(j) - 'org_id' FROM private.dbrole_jobs j ORDER BY j.id;
    SELECT '-- private.sidecars';
    SELECT to_jsonb(s) - 'org_id' FROM private.sidecars s ORDER BY s.id;
    SELECT '-- private.users (without last_login_at)';
    SELECT to_jsonb(u) - 'org_id' - 'last_login_at' FROM private.users u ORDER BY u.subject;
    SELECT '-- private.user_groups';
    SELECT to_jsonb(g) - 'org_id' FROM private.user_groups g ORDER BY g.user_id, g.name;
    SELECT '-- private.org_feature_flags (name, enabled)';
    SELECT jsonb_build_object('name', f.name, 'enabled', f.enabled) FROM private.org_feature_flags f
      JOIN private.orgs o ON o.id = f.org_id WHERE o.name = ${str(ORG_NAME)} ORDER BY f.name;
    SELECT '-- private.orgs (default_protection_profile)';
    SELECT jsonb_build_object('default_protection_profile', default_protection_profile) FROM private.orgs
      WHERE name = ${str(ORG_NAME)};`))
}

// Run as a command; seed.test.mjs imports the SQL builders.
if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const command = process.argv[2] ?? 'seed'
  if (command === 'seed') await seed()
  else if (command === 'empty') await empty()
  else if (command === 'dump') dump()
  else fail(`unknown command "${command}"; use seed, empty or dump`)
}
