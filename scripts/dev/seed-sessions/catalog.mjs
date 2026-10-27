// What the seed writes: users, connections, sessions with their streams and
// reviews, and the sidecar sessions it posts. Every id derives from a name and
// every timestamp is fixed, so two runs write the same rows.
import { createHash } from 'node:crypto'
import {
  RDP_DETECTIONS, RDP_HEIGHT, RDP_WIDTH, binaryStream, event, execStream, httpStream, pgWireStream,
  ptyStream, rdpStream, sshNoPtyStream, sshShellStream,
} from './streams.mjs'

// uuid.NewSHA1(uuid.NameSpaceURL, name), as the gateway derives blob and
// sidecar session ids.
export function uuidv5(name) {
  const ns = Buffer.from('6ba7b8119dad11d180b400c04fd430c8', 'hex')
  const hash = createHash('sha1').update(Buffer.concat([ns, Buffer.from(name)])).digest()
  hash[6] = (hash[6] & 0x0f) | 0x50
  hash[8] = (hash[8] & 0x3f) | 0x80
  const hex = hash.subarray(0, 16).toString('hex')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export const seedId = (kind, key) => uuidv5(`hoop-seed:${kind}:${key}`)

// One password for every seed user. The hash is bcrypt (cost 10) of it, from
// htpasswd -bnBC 10 "" hoop-seed-1234; Go's bcrypt reads the $2y$ prefix.
export const PASSWORD = 'hoop-seed-1234'
export const PASSWORD_HASH = '$2y$10$.IMUTvRmh8v4eKw0rcAyteD9o2wAzs1TBB8yr/LttsrASfhhNKV02'

export const REVIEW_GROUP = 'seed-reviewers'
export const FORCE_GROUP = 'seed-force'
export const DBA_GROUP = 'seed-dba'

// The roles of the behavior matrix legend. adminGroup and auditorGroup are the
// gateway's ADMIN_USERNAME and AUDITOR_USERNAME.
export function users({ adminGroup, auditorGroup }) {
  return {
    admin: { email: 'admin@seed.example.com', name: 'Seed Admin', groups: [adminGroup] },
    auditor: { email: 'auditor@seed.example.com', name: 'Seed Auditor', groups: [auditorGroup] },
    approver: { email: 'approver@seed.example.com', name: 'Seed Approver', groups: [REVIEW_GROUP, FORCE_GROUP] },
    owner: { email: 'owner@seed.example.com', name: 'Seed Owner', groups: [] },
    other: { email: 'other@seed.example.com', name: 'Seed Other', groups: [] },
  }
}

const OWNER = { id: 'owner@seed.example.com', name: 'Seed Owner', email: 'owner@seed.example.com' }
const APPROVER = { id: 'approver@seed.example.com', name: 'Seed Approver', email: 'approver@seed.example.com' }
const MACHINE = { id: 'machine:seed-ci', name: 'seed-ci', email: '' }

// Connections: the ones sessions run on, plus fillers so the Resource Role
// filter has more than one page (the webapp asks 50 at a time).
export const CONNECTIONS = [
  { name: 'seed-bash', resource: 'seed-shell', type: 'custom', subtype: '', command: ['bash'] },
  { name: 'seed-pg', resource: 'seed-postgres', type: 'database', subtype: 'postgres', forceApproveGroups: [FORCE_GROUP] },
  { name: 'seed-mssql', resource: 'seed-mssql', type: 'database', subtype: 'mssql' },
  { name: 'seed-ssh', resource: 'seed-ssh-host', type: 'application', subtype: 'ssh' },
  { name: 'seed-web', resource: 'seed-web', type: 'httpproxy', subtype: 'httpproxy' },
  { name: 'seed-rdp', resource: 'seed-windows', type: 'custom', subtype: 'rdp' },
  { name: 'seed-k8s', resource: 'seed-k8s', type: 'custom', subtype: 'kubernetes', command: ['kubectl', 'exec', '-it'] },
  ...Array.from({ length: 60 }, (_, i) => {
    const n = String(i + 1).padStart(2, '0')
    return { name: `seed-fill-${n}`, resource: `seed-fill-${n}`, type: 'custom', subtype: '', command: ['bash'] }
  }),
]
const connection = (name) => CONNECTIONS.find((c) => c.name === name)

// The sidecar the seed registers by SQL, and the events it posts for it.
export const SIDECAR = {
  id: seedId('sidecar', 'edge'),
  name: 'seed-sidecar',
  token: 'hsc_seed_sidecar_token_not_a_secret',
}

const sidecarEvent = (seq, session, sec, kind, fields = {}) => ({
  seq,
  event: {
    kind,
    timestamp: new Date(Date.parse(session.start) + sec * 1000).toISOString(),
    session_id: session.id,
    principal: 'alice@example.com',
    protocol: 'postgres',
    connection: 'appdb',
    allowed: false,
    ...fields,
  },
})

// sc-full has every event kind that writes (gateway/api/sidecar/events_test.go
// fullSession); sc-simple is the parity check SC-10 session. sc-full starts
// on the first page of /sessions (see LEADING).
const SC_FULL = { id: 'seed-sc-full', start: '2026-10-03T16:32:30Z' }
const SC_SIMPLE = { id: 'seed-sc-simple', start: '2026-10-03T16:12:30Z' }
export const SIDECAR_EVENTS = [
  sidecarEvent(1, SC_FULL, 0, 'session_start'),
  sidecarEvent(2, SC_FULL, 1, 'statement', { statement: 'SELECT * FROM users', allowed: true }),
  sidecarEvent(3, SC_FULL, 2, 'violation', {
    statement: 'DROP TABLE users', rule: 'no-drop', message: 'drops need a review', direction: 'client',
  }),
  sidecarEvent(4, SC_FULL, 3, 'masked', { masked_entities: ['ssn', 'email'], masked_count: 3, direction: 'server' }),
  sidecarEvent(5, SC_FULL, 4, 'error', { error: 'upstream closed the connection' }),
  sidecarEvent(6, SC_FULL, 5, 'session_end', { statement_count: 2, denied_count: 1, duration_ns: 5000000000 }),
  sidecarEvent(1, SC_SIMPLE, 0, 'session_start'),
  sidecarEvent(2, SC_SIMPLE, 1, 'statement', { statement: 'SELECT 1', allowed: true }),
  sidecarEvent(3, SC_SIMPLE, 2, 'session_end', { statement_count: 1, duration_ns: 2000000000 }),
]
export const SIDECAR_SESSIONS = [SC_FULL, SC_SIMPLE].map((s) => ({
  key: s.id,
  id: uuidv5(`sidecar-session:${SIDECAR.id}:${s.id}`),
}))

// ─── Session builders ──────────────────────────────────────────────────────

const iso = (ms) => new Date(ms).toISOString()
// RFC 3339 without milliseconds, as the gateway writes credential times.
const rfc3339 = (at) => at.replace('.000Z', 'Z')
const plus = (at, seconds) => iso(Date.parse(at) + seconds * 1000)

const sizeOf = (stream) => Buffer.byteLength(JSON.stringify(stream))

// base builds a session row. Fields not given keep the gateway's defaults.
// fields.review, and fields.metadata when it is a function, take the
// session's created_at. A large stream (streamSql) gets its event_size in SQL.
function base(key, at, fields) {
  const conn = connection(fields.connection)
  const verb = fields.verb ?? 'exec'
  const stream = fields.stream ?? null
  const ended = fields.status === 'done' || fields.status === undefined
  const hasMetrics = stream || fields.streamSql || fields.metrics
  return {
    key,
    id: seedId('session', key),
    connection: conn.name,
    connection_type: conn.type,
    connection_subtype: conn.subtype,
    verb,
    status: 'done',
    recording_format: verb === 'exec' ? 'exec' : null,
    user: OWNER,
    created_at: at,
    ended_at: ended ? plus(at, fields.duration ?? 2) : null,
    exit_code: verb === 'exec' && ended ? 0 : null,
    origin: 'webapp',
    identity_type: 'user',
    input: '',
    labels: null,
    metadata: null,
    integrations_metadata: null,
    ai_analysis: null,
    guardrails_info: null,
    session_batch_id: null,
    correlation_id: null,
    stream_format: null,
    ...fields,
    review: fields.review ? fields.review(at) : null,
    metadata: typeof fields.metadata === 'function' ? fields.metadata(at) : fields.metadata ?? null,
    metrics: hasMetrics ? { event_size: stream ? sizeOf(stream) : 0, truncated: false, ...fields.metrics } : null,
  }
}

const reviewGroup = (name, status, reviewer, reviewedAt) => ({
  name,
  status,
  reviewer: reviewer ?? null,
  reviewed_at: reviewedAt ?? null,
})

function review(key, at, fields) {
  return {
    id: seedId('review', key),
    type: 'onetime',
    status: 'PENDING',
    created_at: at,
    access_duration_sec: 0,
    time_window: null,
    min_approvals: null,
    force_approval_groups: null,
    access_request_rule_name: null,
    rejection_reason: null,
    revoked_at: null,
    groups: [],
    ...fields,
  }
}

// Kinds are written newest first, five minutes apart: LEADING in its order,
// then the others in the order they are defined. LEADING starts with the kinds
// ENG-900 lists, so the first page of /sessions (20 rows, with
// seed-sc-full) holds one of each but the batch, dated a day earlier;
// seed.test.mjs checks it. The steps of
// seed-wf-short are listed last to first, so they run in order.
const KINDS_START = Date.parse('2026-10-03T18:00:00Z')
const kindAt = (i) => iso(KINDS_START - i * 5 * 60 * 1000)
export const LEADING = [
  'connect-live-pty', 'exec-bash-ok', 'exec-bash-exit-1', 'exec-bash-stderr', 'exec-pg-table', 'exec-pg-error',
  'connect-pty', 'connect-ssh', 'connect-pg-wire', 'connect-http', 'review-pending', 'review-approved',
  'review-rejected', 'review-approved-time-window', 'review-executed', 'exec-output-5mb', 'exec-input-5mb',
  'exec-guardrail-block', 'workflow-short-3', 'workflow-short-2', 'workflow-short-1', 'connect-rdp',
]

const tableOutput = (rows) => rows.map((r) => r.join('\t')).join('\n') + '\n'

function kinds() {
  const defs = []
  const add = (key, fields) => defs.push({ key, fields })

  // Live sessions: connect, open, no end. pty and postgres feed the live tail.
  add('connect-live-pty', {
    connection: 'seed-bash', verb: 'connect', status: 'open', recording_format: 'pty',
    stream: ptyStream().slice(0, 7),
  })
  add('connect-live-pg', {
    connection: 'seed-pg', verb: 'connect', status: 'open', recording_format: 'raw',
    stream: pgWireStream({ live: true }), stream_format: 'wire-proto',
  })

  // Exec: bash and postgres, success, non-zero exit, stderr.
  add('exec-bash-ok', {
    connection: 'seed-bash', input: 'ls -la /var/log',
    stream: execStream([[0.2, 'o', 'total 12\ndrwxr-xr-x 2 root root 4096 Oct  3 12:00 .\n-rw-r----- 1 root adm   812 Oct  3 11:58 syslog\n']]),
  })
  add('exec-bash-exit-1', {
    connection: 'seed-bash', input: 'ls /does-not-exist', exit_code: 1,
    stream: execStream([[0.1, 'e', "ls: cannot access '/does-not-exist': No such file or directory\n"]]),
  })
  add('exec-bash-stderr', {
    connection: 'seed-bash', input: 'apt list --upgradable',
    stream: execStream([
      [0.1, 'e', 'WARNING: apt does not have a stable CLI interface. Use with caution in scripts.\n'],
      [0.4, 'o', 'Listing... Done\nopenssl/stable 3.0.15-1 amd64 [upgradable from: 3.0.14-1]\n'],
    ]),
  })
  add('exec-pg-table', {
    connection: 'seed-pg', input: 'SELECT id, email, plan FROM customers ORDER BY id LIMIT 3;',
    stream: execStream([[0.3, 'o', tableOutput([
      ['id', 'email', 'plan'], ['1', 'ana@example.com', 'free'], ['2', 'bruno@example.com', 'pro'],
      ['3', 'carla@example.com', 'pro'],
    ])]]),
  })
  add('exec-pg-error', {
    connection: 'seed-pg', input: 'SELECT * FROM missing_table;', exit_code: 1,
    stream: execStream([[0.2, 'e', 'ERROR:  relation "missing_table" does not exist\nLINE 1: SELECT * FROM missing_table;\n                      ^\n']]),
  })
  add('exec-pg-empty', {
    connection: 'seed-pg', input: "UPDATE customers SET plan = 'pro' WHERE id = 0;",
    stream: [],
  })
  add('exec-mssql-table', {
    connection: 'seed-mssql', input: 'SELECT TOP 2 id, name FROM dbo.products;',
    stream: execStream([[0.3, 'o', 'Changed database context to \'shop\'.\n' + tableOutput([
      ['id', 'name'], ['10', 'Keyboard'], ['11', 'Mouse'],
    ])]]),
  })
  add('exec-long-lines', {
    connection: 'seed-bash', input: 'cat /var/log/app/trace.log',
    stream: execStream([[0.2, 'o', Array.from({ length: 40 }, (_, i) =>
      i === 3 ? `line ${i} ` + 'x'.repeat(12 * 1024) : `line ${i} trace id=${createHash('md5').update(String(i)).digest('hex')}`,
    ).join('\n') + '\n\n\n']]),
  })
  add('exec-binary-output', {
    connection: 'seed-bash', input: 'cat /tmp/archive.zip', stream: binaryStream(),
  })
  add('exec-output-3mb', {
    connection: 'seed-bash', input: 'cat customers.csv', streamSql: largeOutputSql(40000, 0),
  })
  add('exec-output-5mb', {
    connection: 'seed-pg', input: 'SELECT * FROM events;', streamSql: largeOutputSql(70000, 1),
  })
  add('exec-input-5mb', {
    connection: 'seed-pg', inputSql: largeInputSql(80000),
    stream: execStream([[0.5, 'o', 'INSERT 0 80000\n']]),
  })
  add('exec-input-and-output-5mb', {
    connection: 'seed-pg', inputSql: largeInputSql(80000), streamSql: largeOutputSql(70000, 2),
  })

  // Guardrails: one input deny-words hit; two hits, one of them a pattern.
  add('exec-guardrail-block', {
    connection: 'seed-pg', input: 'DROP TABLE customers;', exit_code: 1,
    stream: execStream([[0.1, 'e', 'blocked by guardrail: no-drop\n']]),
    guardrails_info: [{
      rule_name: 'no-drop', rule: { type: 'deny_words_list', words: ['drop'] }, direction: 'input',
      matched_words: ['DROP'], message: 'Dropping tables needs an access request.',
    }],
  })
  add('exec-guardrail-multi', {
    connection: 'seed-pg', input: 'SELECT ssn, email FROM customers; DELETE FROM customers;', exit_code: 1,
    stream: execStream([[0.1, 'e', 'blocked by guardrails\n']]),
    guardrails_info: [
      {
        rule_name: 'no-delete', rule: { type: 'deny_words_list', words: ['delete', 'truncate'] },
        direction: 'input', matched_words: ['DELETE'], message: '',
      },
      {
        rule_name: 'no-ssn', rule: { type: 'pattern_match', pattern_regex: '\\d{3}-\\d{2}-\\d{4}' },
        direction: 'output', matched_words: ['123-45-6789'], message: 'Social security numbers stay inside.',
      },
    ],
  })

  // Fields of the details page.
  add('exec-runbook', {
    connection: 'seed-bash', origin: 'runbooks', input: 'systemctl restart api',
    labels: { runbookFile: 'ops/restart-service.runbook.sh', runbookParameters: '{"service":"api","force":"true"}' },
    stream: execStream([[1, 'o', 'restarted api\n']]),
  })
  add('exec-jira', {
    connection: 'seed-pg', input: 'SELECT count(*) FROM orders;',
    integrations_metadata: { jira_issue_key: 'SEED-42', jira_issue_url: 'https://example.atlassian.net/browse/SEED-42' },
    stream: execStream([[0.2, 'o', tableOutput([['count'], ['1287']])]]),
  })
  add('exec-ai-blocked', {
    connection: 'seed-pg', input: 'DELETE FROM payments WHERE created_at < now();', exit_code: 1,
    stream: execStream([[0.1, 'e', 'blocked by the AI session analyzer\n']]),
    ai_analysis: {
      risk_level: 'high', title: 'Bulk delete of payment history', action: 'block_execution',
      explanation: 'The statement deletes every payment row older than now, which is the whole table.',
      summary: 'A DELETE without a bounded filter on a financial table.', model: 'seed-model',
      steps: [
        { type: 'thinking', thinking: 'The filter created_at < now() matches every existing row.', timestamp: '2026-10-03T16:30:00Z' },
        { type: 'tool_call', tool_name: 'count_rows', tool_input: '{"table":"payments"}', timestamp: '2026-10-03T16:30:01Z' },
        { type: 'tool_result', tool_name: 'count_rows', tool_output: '{"rows":48211}', timestamp: '2026-10-03T16:30:02Z' },
        { type: 'tool_call', tool_name: 'list_backups', tool_input: '{}', timestamp: '2026-10-03T16:30:03Z' },
        { type: 'tool_result', tool_name: 'list_backups', tool_output: 'permission denied', is_error: true, timestamp: '2026-10-03T16:30:04Z' },
      ],
    },
  })
  add('exec-ai-allowed', {
    connection: 'seed-pg', input: 'SELECT id FROM payments WHERE id = 7;',
    stream: execStream([[0.2, 'o', tableOutput([['id'], ['7']])]]),
    ai_analysis: {
      risk_level: 'low', title: 'Single-row read', action: 'allow_execution',
      explanation: 'A read by primary key.', summary: 'Reads one payment.',
    },
  })
  add('exec-metadata', {
    connection: 'seed-bash', input: 'uptime',
    metadata: {
      reason: 'Investigate the 503 errors', ticket: 'https://example.com/tickets/1234',
      credentials_expire_at: '2026-10-03T20:00:00Z', sidecar_session_id: 'hidden-key',
    },
    stream: execStream([[0.1, 'o', ' 16:00:01 up 42 days,  load average: 0.08\n']]),
  })
  add('exec-data-masking', {
    connection: 'seed-pg', input: 'SELECT name, email, phone FROM customers LIMIT 2;',
    stream: execStream([[0.3, 'o', tableOutput([
      ['name', 'email', 'phone'], ['<PERSON>', '<EMAIL_ADDRESS>', '<PHONE_NUMBER>'], ['<PERSON>', '<EMAIL_ADDRESS>', '<PHONE_NUMBER>'],
    ])]]),
    metrics: {
      data_analyzer: { PERSON: 2, EMAIL_ADDRESS: 2, PHONE_NUMBER: 2 },
      data_masking: {
        err_count: 0, transformed_bytes: 96, total_redact_count: 6,
        info_types: { PERSON: 2, EMAIL_ADDRESS: 2, PHONE_NUMBER: 2 },
      },
    },
    session_metrics: [
      { info_type: 'EMAIL_ADDRESS', count_masked: 2, count_analyzed: 2 },
      { info_type: 'PERSON', count_masked: 2, count_analyzed: 2 },
      { info_type: 'PHONE_NUMBER', count_masked: 2, count_analyzed: 2 },
    ],
  })

  // Reviews. The pending ones wait for seed-reviewers; seed-pg makes
  // seed-force a force-approve group.
  add('review-pending', {
    connection: 'seed-pg', input: 'UPDATE customers SET plan = \'free\' WHERE id = 3;',
    status: 'open', stream: null,
    review: (at) => review('review-pending', at, {
      force_approval_groups: [FORCE_GROUP],
      groups: [reviewGroup(REVIEW_GROUP, 'PENDING')],
    }),
  })
  add('review-pending-time-window', {
    connection: 'seed-bash', input: 'systemctl restart nginx',
    status: 'open', stream: null,
    review: (at) => review('review-pending-time-window', at, {
      time_window: { type: 'time_range', configuration: { start_time: '22:00', end_time: '23:30' } },
      groups: [reviewGroup(REVIEW_GROUP, 'PENDING')],
    }),
  })
  add('review-approved', {
    connection: 'seed-pg', input: 'VACUUM ANALYZE customers;',
    status: 'ready', stream: null,
    review: (at) => review('review-approved', at, {
      status: 'APPROVED',
      groups: [reviewGroup(REVIEW_GROUP, 'APPROVED', APPROVER, plus(at, 60))],
    }),
  })
  add('review-approved-time-window', {
    connection: 'seed-bash', input: 'reboot',
    status: 'ready', stream: null,
    review: (at) => review('review-approved-time-window', at, {
      status: 'APPROVED',
      time_window: { type: 'time_range', configuration: { start_time: '02:00', end_time: '04:00' } },
      groups: [reviewGroup(REVIEW_GROUP, 'APPROVED', APPROVER, plus(at, 90))],
    }),
  })
  // A rejection sets the status only (gateway/models/reviews.go): no end.
  add('review-rejected', {
    connection: 'seed-pg', input: 'TRUNCATE audit_log;',
    status: 'done', ended_at: null, exit_code: null, stream: null,
    review: (at) => review('review-rejected', at, {
      status: 'REJECTED',
      rejection_reason: 'Not during business hours.\n  Retry after 18:00 with a ticket.',
      groups: [reviewGroup(REVIEW_GROUP, 'REJECTED', APPROVER, plus(at, 120))],
    }),
  })
  add('review-executed', {
    connection: 'seed-pg', input: "DELETE FROM sessions_cache WHERE expires_at < '2026-10-01';",
    stream: execStream([[0.4, 'o', 'DELETE 312\n']]),
    review: (at) => review('review-executed', at, {
      status: 'EXECUTED',
      groups: [reviewGroup(REVIEW_GROUP, 'APPROVED', APPROVER, plus(at, 30))],
    }),
  })
  add('review-two-groups', {
    connection: 'seed-pg', input: 'ALTER TABLE customers ADD COLUMN tier text;',
    status: 'open', stream: null,
    review: (at) => review('review-two-groups', at, {
      min_approvals: 2,
      groups: [
        reviewGroup(REVIEW_GROUP, 'APPROVED', APPROVER, plus(at, 45)),
        reviewGroup(DBA_GROUP, 'PENDING'),
      ],
    }),
  })

  // Connect sessions, one per output viewer.
  add('connect-pty', {
    connection: 'seed-bash', verb: 'connect', recording_format: 'pty', stream: ptyStream(), duration: 9,
  })
  add('connect-ssh', {
    connection: 'seed-ssh', verb: 'connect', recording_format: 'ssh', stream: sshShellStream(), duration: 6,
  })
  add('connect-ssh-no-pty', {
    connection: 'seed-ssh', verb: 'connect', recording_format: 'ssh', stream: sshNoPtyStream(),
  })
  add('connect-ssh-legacy-pty', {
    connection: 'seed-ssh', verb: 'connect', recording_format: 'ssh', stream: ptyStream(),
  })
  add('connect-pg-wire', {
    connection: 'seed-pg', verb: 'connect', recording_format: 'raw', stream: pgWireStream(),
    stream_format: 'wire-proto', duration: 5,
  })
  add('connect-http', {
    connection: 'seed-web', verb: 'connect', recording_format: 'raw', stream: httpStream(),
  })
  add('connect-legacy-k8s', {
    connection: 'seed-k8s', verb: 'connect', recording_format: null, stream: ptyStream(), origin: 'cli',
  })
  const rdp = rdpStream()
  add('connect-rdp', {
    connection: 'seed-rdp', verb: 'connect', recording_format: 'rdp', stream: rdp, duration: 10, exit_code: 0,
    metrics: {
      bitmap_count: rdp.filter((e) => e[1] === 'b').length, canvas_width: RDP_WIDTH, canvas_height: RDP_HEIGHT,
    },
    rdp_job: { status: 'done', attempt: 1, last_error: null },
    rdp_detections: RDP_DETECTIONS,
  })
  add('connect-rdp-analysis-failed', {
    connection: 'seed-rdp', verb: 'connect', recording_format: 'rdp', stream: rdp, duration: 10, exit_code: 0,
    metrics: {
      bitmap_count: rdp.filter((e) => e[1] === 'b').length, canvas_width: RDP_WIDTH, canvas_height: RDP_HEIGHT,
    },
    rdp_job: { status: 'failed', attempt: 3, last_error: 'presidio analyzer: connection refused' },
  })
  add('connect-rdp-no-frames', {
    connection: 'seed-rdp', verb: 'connect', recording_format: 'rdp', stream: [event(0, 'h', 'seed-rdp-handshake')],
    exit_code: 0, metrics: { bitmap_count: 0, canvas_width: 1280, canvas_height: 720 },
  })

  // Just-in-time access to a native connection.
  add('jit-ready', {
    connection: 'seed-pg', verb: 'connect', status: 'ready', stream: null, recording_format: 'raw',
    review: (at) => review('jit-ready', at, {
      type: 'jit', status: 'APPROVED', access_duration_sec: 1800,
      groups: [reviewGroup(REVIEW_GROUP, 'APPROVED', APPROVER, plus(at, 20))],
    }),
  })
  // A revocation ends the session at the revocation time.
  add('jit-revoked', {
    connection: 'seed-pg', verb: 'connect', recording_format: 'raw', stream: null, duration: 600,
    metadata: (at) => ({ credentials_revoked_at: rfc3339(plus(at, 600)) }),
    review: (at) => review('jit-revoked', at, {
      type: 'jit', status: 'APPROVED', access_duration_sec: 3600,
      groups: [reviewGroup(REVIEW_GROUP, 'APPROVED', APPROVER, plus(at, 20))],
    }),
  })
  add('jit-expired', {
    connection: 'seed-pg', verb: 'connect', recording_format: 'raw', status: 'ready', stream: null,
    metadata: (at) => ({ credentials_expire_at: rfc3339(plus(at, 600)) }),
    review: (at) => review('jit-expired', at, {
      type: 'jit', status: 'APPROVED', access_duration_sec: 600,
      groups: [reviewGroup(REVIEW_GROUP, 'APPROVED', APPROVER, plus(at, 20))],
    }),
  })
  add('jit-ended', {
    connection: 'seed-pg', verb: 'connect', recording_format: 'raw', stream: pgWireStream(), stream_format: 'wire-proto',
    review: (at) => review('jit-ended', at, {
      type: 'jit', status: 'APPROVED', access_duration_sec: 900,
      groups: [reviewGroup(REVIEW_GROUP, 'APPROVED', APPROVER, plus(at, 20))],
    }),
  })
  add('credentials-link', {
    connection: 'seed-pg', verb: 'connect', recording_format: 'raw', stream: null,
    metadata: { credential_session: seedId('session', 'jit-ended') },
  })

  // A short workflow: Running, Failed and Success steps, one by a machine.
  const short = 'seed-wf-short'
  add('workflow-short-1', {
    connection: 'seed-bash', input: 'make deploy', correlation_id: short, stream: execStream([[1, 'o', 'deployed\n']]),
  })
  add('workflow-short-2', {
    connection: 'seed-pg', input: 'SELECT migrate();', correlation_id: short, exit_code: 2,
    user: MACHINE, identity_type: 'machine', origin: 'api',
    stream: execStream([[0.5, 'e', 'ERROR:  migration 42 failed\n']]),
  })
  add('workflow-short-3', {
    connection: 'seed-bash', input: 'make smoke-test', correlation_id: short, status: 'open', stream: null,
  })

  const rank = (key) => (LEADING.includes(key) ? LEADING.indexOf(key) : LEADING.length)
  return defs
    .map((d, i) => ({ ...d, i }))
    .sort((a, b) => rank(a.key) - rank(b.key) || a.i - b.i)
    .map(({ key, fields }, i) => base(key, kindAt(i), fields))
}

// A large exec output in one event, built in SQL: `lines` CSV-like lines.
function largeOutputSql(lines, salt) {
  return `jsonb_build_array(jsonb_build_array(0.5, 'o', translate(encode(convert_to((
    SELECT string_agg(g || ',customer-' || g || '@example.com,' || md5((g + ${salt})::text), E'\\n')
    FROM generate_series(1, ${lines}) AS g) || E'\\n', 'UTF8'), 'base64'), E'\\n', '')))`
}

// A large exec input, built in SQL: `lines` INSERT statements.
function largeInputSql(lines) {
  return `jsonb_build_array((
    SELECT string_agg('INSERT INTO audit_log (id, note) VALUES (' || g || ', ''seeded row ' || g || ''');', E'\\n')
    FROM generate_series(1, ${lines}) AS g))`
}

// A batch (parallel run) of 25 sessions, more than one page of 20.
function batch() {
  const start = Date.parse('2026-10-02T10:00:00Z')
  const conns = ['seed-pg', 'seed-mssql', 'seed-bash']
  return Array.from({ length: 25 }, (_, i) => {
    const n = String(i + 1).padStart(2, '0')
    const failed = i % 8 === 7
    return base(`batch-${n}`, iso(start + i * 1000), {
      connection: conns[i % conns.length], session_batch_id: 'seed-batch-1', origin: 'webapp',
      input: 'SELECT count(*) FROM pg_stat_activity;', exit_code: failed ? 1 : 0,
      stream: execStream([[0.2, failed ? 'e' : 'o', failed ? 'ERROR:  permission denied\n' : `count\n${i + 3}\n`]]),
    })
  })
}

// A workflow of 210 steps: more than the 200 the timeline asks for. The API
// returns at most 100 (MaxSessionListLimit).
function workflow() {
  const start = Date.parse('2026-10-01T08:00:00Z')
  return Array.from({ length: 210 }, (_, i) => {
    const n = String(i + 1).padStart(3, '0')
    const running = i >= 207
    const failed = i % 15 === 14
    const machine = i % 20 === 0
    return base(`workflow-${n}`, iso(start + i * 60 * 1000), {
      connection: i % 2 ? 'seed-pg' : 'seed-bash', correlation_id: 'seed-workflow-210-steps',
      input: `./step.sh ${i + 1}`, status: running ? 'open' : 'done', exit_code: running ? null : failed ? 1 : 0,
      ...(machine ? { user: MACHINE, identity_type: 'machine', origin: 'api' } : {}),
      stream: running ? null : execStream([[0.3, failed ? 'e' : 'o', failed ? `step ${i + 1} failed\n` : `step ${i + 1} ok\n`]]),
    })
  })
}

export function sessions() {
  return [...kinds(), ...batch(), ...workflow()]
}
