import { Box, Divider, Grid, Group, Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import { MODE_OBSERVE, SOURCE_LISTENER, resolveListener } from '../resolve'
import { supportsGRPCBlock, supportsHTTPBlock } from '../listeners'

// The nine rule types of sidecar/policy. NOT the two in
// pages/Guardrails/helpers.js — those belong to the gateway's own guardrails,
// a different product with a different schema.
const RULE_TYPE_LABELS = {
  deny_words_list: 'Deny words',
  pattern_match: 'Pattern match',
  operation: 'Operation',
  table: 'Table',
  pii: 'PII',
  ai_analysis: 'AI analysis',
  http_resource: 'HTTP resource',
  http_status: 'HTTP status',
  grpc_status: 'gRPC status',
}

const MASK_STRATEGY_LABELS = {
  redact: 'Redact',
  mask: 'Mask',
  partial: 'Partial',
  hash: 'Hash',
}

const NONE = '—'

const join = (v) => (Array.isArray(v) && v.length > 0 ? v.join(', ') : null)

// The part of a rule that says what it matches. Each type reads its own
// fields and ignores the rest (policy.Rule is one struct for all nine).
function ruleMatcher(rule) {
  switch (rule.type) {
    case 'operation':
      return join(rule.operations)
    case 'table': {
      const tables = join(rule.tables)
      if (!tables) return null
      return rule.access ? `${tables} (${rule.access})` : tables
    }
    case 'deny_words_list':
      return join(rule.words)
    case 'pattern_match':
      return rule.pattern_regex || null
    case 'pii':
      return join(rule.entities)
    case 'http_resource':
      return [join(rule.methods), join(rule.resources)].filter(Boolean).join(' ') || null
    case 'http_status':
    case 'grpc_status':
      return [join(rule.methods), join(rule.statuses)].filter(Boolean).join(' ') || null
    case 'ai_analysis':
      return (
        join([rule.trigger?.operations, rule.trigger?.tables, rule.trigger?.resources].map(join).filter(Boolean)) ||
        null
      )
    default:
      return null
  }
}

function Field({ label, children }) {
  return (
    <Group gap="xs" align="baseline" wrap="nowrap">
      <Text size="xs" c="dimmed" w={110} flex="0 0 auto">
        {label}
      </Text>
      <Text size="xs">{children}</Text>
    </Group>
  )
}

function Section({ title, right, children }) {
  return (
    <Stack gap="xs">
      <Group gap="sm" align="baseline">
        <Text size="sm" fw={600}>
          {title}
        </Text>
        {right}
      </Group>
      {children}
    </Stack>
  )
}

function SourceBadge({ source }) {
  const own = source === SOURCE_LISTENER
  return (
    <Badge variant="light" color={own ? 'indigo' : 'gray'} tt="none" fw={500}>
      {own ? 'Listener' : 'Inherited'}
    </Badge>
  )
}

function GuardrailRule({ entry }) {
  const { rule, source } = entry
  const matcher = ruleMatcher(rule)
  return (
    <Stack gap={2}>
      <Group gap="xs" wrap="nowrap">
        <Text size="xs" fw={600}>
          {rule.name || 'Unnamed rule'}
        </Text>
        <SourceBadge source={source} />
        {/* action: "defer" reports a finding instead of denying. */}
        {rule.action === 'defer' && (
          <Badge variant="light" color="yellow" tt="none" fw={500}>
            Report only
          </Badge>
        )}
      </Group>
      <Text size="xs" c="dimmed">
        {[RULE_TYPE_LABELS[rule.type] ?? rule.type, matcher].filter(Boolean).join(' · ')}
      </Text>
    </Stack>
  )
}

function MaskRule({ entry }) {
  const { rule, source } = entry
  const target = join(rule.columns) || join(rule.entities) || (rule.entity ?? null)
  const strategy = MASK_STRATEGY_LABELS[rule.strategy] ?? MASK_STRATEGY_LABELS.redact
  // keep_last only means anything to the partial strategy; its default is 4.
  const keepLast = rule.strategy === 'partial' ? `keep last ${rule.keep_last ?? 4}` : null
  return (
    <Stack gap={2}>
      <Group gap="xs" wrap="nowrap">
        <Text size="xs" fw={600}>
          {rule.name || 'Unnamed rule'}
        </Text>
        <SourceBadge source={source} />
      </Group>
      <Text size="xs" c="dimmed">
        {[target, strategy, keepLast].filter(Boolean).join(' · ')}
      </Text>
    </Stack>
  )
}

function TLSSummary({ tls, downstream }) {
  if (!tls) return NONE
  const parts = []
  if (downstream) {
    if (tls.cert_file) parts.push(tls.cert_file)
  } else {
    parts.push(tls.ca_file ? `CA ${tls.ca_file}` : 'host trust store')
    if (tls.server_name) parts.push(`SNI ${tls.server_name}`)
    if (tls.cert_file) parts.push('mTLS')
  }
  return (
    <Group gap="xs" wrap="nowrap" component="span">
      <span>{parts.join(' · ') || 'On'}</span>
      {tls.insecure_skip_verify && (
        <Badge variant="danger" tt="none" fw={500}>
          Verification off
        </Badge>
      )}
    </Group>
  )
}

/**
 * Everything about one lane that the table's five columns do not say.
 *
 * Two halves. The left is the listener's own configuration, which the form
 * edits. The right is what it RESOLVES to — the guardrails, masking and OPA
 * the daemon actually applies to this lane, which the form does not edit and
 * which has no other home in the UI: today they reduce to a Features chip, so
 * an operator reads the YAML to learn which rules run where.
 *
 * Every rule is marked Listener or Inherited, because the merge is not a union:
 * guardrails concatenate, masking and OPA replace, and each has a spelling that
 * means "none" rather than "inherit" (resolve.js holds the rules).
 */
export default function ListenerDetails({ listener, config }) {
  const { mode, guardrails, mask, opa } = resolveListener(listener, config)
  const observing = mode === MODE_OBSERVE

  return (
    <Box bg="gray.0" p="md">
      <Grid gutter="xl">
        <Grid.Col span={{ base: 12, md: 5 }}>
          <Section title="Configuration">
            <Stack gap={4}>
              <Field label="Transport">{listener.network === 'unix' ? 'Unix socket' : 'TCP'}</Field>
              <Field label="Max conns">{listener.max_conns || 'Unlimited'}</Field>
              <Field label="Idle timeout">
                {listener.idle_timeout_sec ? `${listener.idle_timeout_sec}s` : 'Disabled'}
              </Field>
              <Field label="Upstream TLS">
                <TLSSummary tls={listener.upstream_tls} />
              </Field>
              <Field label="Client TLS">
                <TLSSummary tls={listener.downstream_tls} downstream />
              </Field>
              {supportsHTTPBlock(listener.protocol) && (
                <>
                  <Field label="Identity header">{listener.identity_header || NONE}</Field>
                  <Field label="Capture body">{listener.http?.capture_body ? 'Yes' : 'No'}</Field>
                  <Field label="Headers">{join(listener.http?.headers) || NONE}</Field>
                </>
              )}
              {supportsGRPCBlock(listener.protocol) && (
                <>
                  <Field label="Descriptors">{join([].concat(listener.grpc?.descriptors ?? [])) || NONE}</Field>
                  <Field label="Capture payload">{listener.grpc?.capture_payload ? 'Yes' : 'No'}</Field>
                  <Field label="Strict">{listener.grpc?.strict ? 'Yes' : 'No'}</Field>
                  <Field label="Metadata">{join(listener.grpc?.metadata) || NONE}</Field>
                </>
              )}
            </Stack>
          </Section>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 7 }}>
          <Stack gap="md">
            <Section
              title="Guardrails"
              right={
                <Badge variant={observing ? 'warning' : 'inactive'} tt="none" fw={500}>
                  {observing ? 'Observe' : 'Enforce'}
                </Badge>
              }
            >
              {guardrails.length === 0 ? (
                <Text size="xs" c="dimmed">
                  {observing ? 'No rules. Nothing is evaluated.' : 'No rules. Everything passes.'}
                </Text>
              ) : (
                <Stack gap="xs">
                  {guardrails.map((entry, i) => (
                    <GuardrailRule key={`${entry.rule.name}-${i}`} entry={entry} />
                  ))}
                </Stack>
              )}
            </Section>

            <Divider color="gray.2" />

            <Section title="Masking">
              {mask.length === 0 ? (
                <Text size="xs" c="dimmed">
                  No rules. Responses are returned unchanged.
                </Text>
              ) : (
                <Stack gap="xs">
                  {mask.map((entry, i) => (
                    <MaskRule key={`${entry.rule.name}-${i}`} entry={entry} />
                  ))}
                </Stack>
              )}
            </Section>

            <Divider color="gray.2" />

            <Section title="OPA">
              {opa ? (
                <Stack gap={4}>
                  <Field label="Endpoint">{opa.url}</Field>
                  <Field label="On failure">{opa.fail_open ? 'Allow' : 'Deny'}</Field>
                  <Field label="Gate">{opa.gate ? 'Before the analyzer' : 'After local rules'}</Field>
                </Stack>
              ) : (
                <Text size="xs" c="dimmed">
                  Not used.
                </Text>
              )}
            </Section>
          </Stack>
        </Grid.Col>
      </Grid>
    </Box>
  )
}
