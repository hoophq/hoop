// The sidecar's own rule vocabulary, for the three control-plane rule forms.
//
// It is NOT the gateway's. The two products share three feature names and
// nothing under them: the gateway masks through a DLP provider with entity
// groups and a score threshold, a sidecar masks a decoded response frame with
// entities or column names and a strategy; the gateway knows two guardrail
// rule types, a sidecar knows seven; the gateway's analyzer answers
// allow_execution / block_execution, a sidecar's answers allow / warn / block
// / defer. The docs say so outright:
// hoop.dev/docs/features/data-masking — "That is a different implementation".
//
// Source of truth, in order: the daemon's own Go types, then
// hoop.dev/docs/setup/configuration/hoop-sidecar/{policy-rules,config-file,
// risk-analysis} and protocols/ssh/configuration. The gateway validates every
// save against those same Go types, so anything wrong here is refused with the
// daemon's own message rather than saved.

// ---------------------------------------------------------------------------
// Guardrails
// ---------------------------------------------------------------------------

// The seven rule types, with the fields each one reads. `protocols` narrows
// which lanes may carry it: a sidecar REFUSES a rule its lane cannot read,
// at startup, rather than skipping it at evaluation.
//
// No prose here. What each type matches on is the docs' job, and the form
// links to them — a paragraph per type made the page unreadable and still
// said less than the reference does.
export const GUARDRAIL_RULE_TYPES = [
  {
    value: 'operation',
    label: 'Operation',
    fields: ['operations'],
  },
  {
    value: 'table',
    label: 'Table',
    fields: ['tables', 'access', 'require_table_match'],
    deniedOn: ['ssh'],
  },
  {
    value: 'deny_words_list',
    label: 'Deny words',
    fields: ['words'],
  },
  {
    value: 'pattern_match',
    label: 'Pattern match',
    fields: ['pattern_regex'],
  },
  {
    value: 'pii',
    label: 'PII',
    fields: ['entities'],
  },
  {
    value: 'http_resource',
    label: 'HTTP resource',
    fields: ['resources', 'methods'],
    protocols: ['http'],
  },
  {
    value: 'http_status',
    label: 'HTTP status',
    fields: ['statuses', 'methods'],
    protocols: ['http'],
  },
]

// Every operations value, by the protocol that produces it. The field is the
// whole match on an `operation` rule and a SCOPE on every other type — which
// is load-bearing on an SSH lane, where exec_line's text is a command,
// env_set's is a variable name and every sftp_*'s is a path.
export const OPERATIONS = {
  sql: [
    'select', 'insert', 'update', 'delete', 'merge', 'create', 'drop', 'alter',
    'truncate', 'grant', 'revoke', 'copy', 'call', 'show', 'set', 'begin',
    'commit', 'rollback', 'explain', 'other', 'unknown',
  ],
  http: ['get', 'post', 'put', 'patch', 'head', 'options', 'connect', 'trace'],
  ssh: [
    'exec_line', 'env_set', 'sftp_read', 'sftp_write', 'sftp_remove',
    'sftp_rename', 'sftp_mkdir', 'sftp_rmdir', 'sftp_list', 'sftp_stat',
    'sftp_setstat', 'sftp_symlink',
  ],
}

// Which operations family a listener protocol produces.
export function operationsFor(protocol) {
  const p = (protocol ?? '').toLowerCase()
  if (p === 'ssh') return OPERATIONS.ssh
  if (p === 'http' || p === 'grpc') return OPERATIONS.http
  return OPERATIONS.sql
}

export const TABLE_ACCESS = [
  { value: '', label: 'Read or write' },
  { value: 'read', label: 'Read only' },
  { value: 'write', label: 'Write only' },
]

export const GUARDRAIL_ACTIONS = [
  { value: '', label: 'Deny the statement' },
  { value: 'defer', label: 'Report a finding, let Rego decide' },
]

// ---------------------------------------------------------------------------
// Data masking
// ---------------------------------------------------------------------------

// The `help` here stays, unlike the guardrail types': a before/after on a real
// value is what an operator picks a strategy on, and no label can carry it.
export const MASK_STRATEGIES = [
  { value: 'redact', label: 'Redact', help: '4111111111111111 → [REDACTED:CREDIT_CARD]' },
  { value: 'mask', label: 'Mask', help: '4111111111111111 → ****************' },
  { value: 'partial', label: 'Partial', help: '4111111111111111 → ************1111' },
  { value: 'hash', label: 'Hash', help: '4111111111111111 → sha256:19d25e4ad4f3a1c2 (equal inputs still join)' },
]

// An ssh lane rewrites a byte stream in place, so only a length-preserving
// strategy can run there. The gateway refuses the rest with the daemon's own
// message; this is what keeps the form from offering them in the first place.
export const SSH_ONLY_STRATEGY = 'mask'

// ---------------------------------------------------------------------------
// AI analyzer
// ---------------------------------------------------------------------------

// An unset level and an explicit `allow` do the same thing, and the list has
// both because omitting the key keeps it out of the served document. They need
// different LABELS: two entries reading "Allow" in one dropdown give the
// operator no way to tell which one they picked.
export const ANALYZER_ACTIONS = [
  { value: '', label: 'Not set (allows)' },
  { value: 'allow', label: 'Allow' },
  { value: 'warn', label: 'Warn' },
  { value: 'block', label: 'Block' },
  { value: 'defer', label: 'Defer to Rego' },
]

// ---------------------------------------------------------------------------
// Shared
// ---------------------------------------------------------------------------

// Entity types both the guardrail `pii` rule and a mask rule name. The
// detector supports more; these are the ones worth offering, and the field
// accepts anything the operator types.
export const ENTITY_TYPES = [
  'EMAIL_ADDRESS', 'CREDIT_CARD', 'IBAN_CODE', 'PHONE_NUMBER', 'IP_ADDRESS',
  'US_SSN', 'BR_CPF', 'BR_CNPJ', 'AWS_ACCESS_KEY', 'PRIVATE_KEY',
  'PERSON', 'LOCATION', 'DATE_TIME', 'URL',
]

// ruleTypesFor narrows the type list to what a set of bound listeners can all
// carry. Binding to one ssh lane and one postgres lane leaves the types both
// accept, which is the honest answer: a rule reaching a lane that refuses it
// takes that whole sidecar's configuration down at its next restart.
export function ruleTypesFor(protocols) {
  const lanes = protocols.filter(Boolean).map((p) => p.toLowerCase())
  if (lanes.length === 0) return GUARDRAIL_RULE_TYPES
  return GUARDRAIL_RULE_TYPES.filter((t) => {
    if (t.deniedOn?.some((p) => lanes.includes(p))) return false
    if (t.protocols && !lanes.every((p) => t.protocols.includes(p))) return false
    return true
  })
}
