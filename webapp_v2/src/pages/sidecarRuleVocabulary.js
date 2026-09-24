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

// The eight rule types, with the fields each one reads. `protocols` narrows
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
  {
    // The gRPC twin of http_status, and it reads a trailer rather than a
    // header: a gRPC call answers 200 and carries its real outcome in
    // grpc-status, so http_status matches nothing on these lanes. Spanner is
    // here too — it is GoogleSQL over the same gRPC transport, so the same
    // status metadata is on its statements.
    value: 'grpc_status',
    label: 'gRPC status',
    fields: ['statuses'],
    protocols: ['grpc', 'spanner'],
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
  { value: 'redact', label: 'Redact' },
  { value: 'mask', label: 'Mask' },
  { value: 'partial', label: 'Partial' },
  { value: 'hash', label: 'Hash' },
]

// The value every preview rewrites: a card number, because it is what an
// operator pictures, and sixteen digits make a keep_last of four read at a
// glance.
const MASK_SAMPLE = '4111111111111111'

// The real first 16 hex digits of sha256(MASK_SAMPLE). A made-up digest in a
// field that invites the reader to check it is worse than no example.
const MASK_SAMPLE_DIGEST = '9bbef19476623ca5'

// The daemon's own defaults (sidecar/pii/alcatraz/masker.go), for the two
// fields the form leaves empty to inherit them.
export const DEFAULT_MASK_CHAR = '*'
export const DEFAULT_KEEP_LAST = 4

// maskPreview renders what the lane would return for MASK_SAMPLE.
//
// It reproduces the masker's operators rather than approximating them, so an
// operator reading the field learns the real rule. The one worth seeing is
// `partial`: it masks only LETTERS AND DIGITS before the tail and leaves
// separators in place, because a separator is format rather than data. And a
// keep_last at or above the value's length masks the whole thing instead of
// passing it through — the safe end, and the opposite of what "keep this many"
// suggests.
export function maskPreview(strategy, { maskChar, keepLast } = {}) {
  const ch = maskChar || DEFAULT_MASK_CHAR
  const chars = [...MASK_SAMPLE]

  switch (strategy) {
    case 'redact':
      return `${MASK_SAMPLE} → [REDACTED:CREDIT_CARD]`
    case 'mask':
      // Every character, separators included, with the rune count preserved.
      return `${MASK_SAMPLE} → ${ch.repeat(chars.length)}`
    case 'partial': {
      // Zero is not zero. The daemon reads an unset keep_last as the default,
      // and `omitempty` means a 0 never reaches it as a 0 anyway — so an
      // operator who types 0 expecting the whole value masked gets the last
      // four in the clear. The preview shows that rather than describing it.
      const raw = Number(keepLast)
      const keep = Number.isFinite(raw) && raw > 0 ? raw : DEFAULT_KEEP_LAST
      const cut = keep >= chars.length ? chars.length : chars.length - keep
      const out = chars
        .map((c, i) => (i >= cut ? c : /[\p{L}\p{N}]/u.test(c) ? ch : c))
        .join('')
      return `${MASK_SAMPLE} → ${out}`
    }
    case 'hash':
      return `${MASK_SAMPLE} → sha256:${MASK_SAMPLE_DIGEST} (equal inputs still join)`
    default:
      return ''
  }
}

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

// The action that holds a statement for a human. It needs a second object
// behind it (the rule naming who may release), which the form writes beside
// it. Every protocol holds (EVL-312).
export const REVIEW_ACTION = 'require_review'

// analyzerActionsFor lists the level actions, the hold included.
export function analyzerActionsFor() {
  return [...ANALYZER_ACTIONS, { value: REVIEW_ACTION, label: 'Hold for approval' }]
}

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
