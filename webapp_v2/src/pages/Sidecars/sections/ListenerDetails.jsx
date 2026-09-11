import { Box, Divider, Group, Stack, Text } from '@mantine/core'
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
        [rule.trigger?.operations, rule.trigger?.tables, rule.trigger?.resources]
          .map(join)
          .filter(Boolean)
          .join(', ') || null
      )
    default:
      return null
  }
}

/**
 * The few configuration facts worth reading without opening the form.
 *
 * Deliberately not the whole listener: certificate paths, body limits and
 * codec switches are the form's job, and a reader who wants them opens Edit.
 * What survives is what an operator asks at a glance — how the lane is
 * reached, whether either hop is encrypted, and any limit that is actually
 * set. A limit left at its default says nothing and is left out.
 */
function configChips(listener) {
  const chips = [listener.network === 'unix' ? 'Unix socket' : 'TCP']
  if (listener.upstream_tls) chips.push('Upstream TLS')
  if (listener.downstream_tls) chips.push('Client TLS')
  if (listener.max_conns) chips.push(`max ${listener.max_conns} conns`)
  if (listener.idle_timeout_sec) chips.push(`idle ${listener.idle_timeout_sec}s`)
  if (supportsHTTPBlock(listener.protocol) && listener.http?.capture_body) chips.push('Body captured')
  if (supportsGRPCBlock(listener.protocol) && listener.grpc?.capture_payload) chips.push('Payload captured')
  if (listener.upstream_tls?.insecure_skip_verify) chips.push('Verification off')
  return chips
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

function Rule({ name, detail, source, extra }) {
  return (
    <Group gap="sm" align="baseline" wrap="nowrap">
      <Text size="xs" fw={600}>
        {name || 'Unnamed rule'}
      </Text>
      <Text size="xs" c="dimmed" flex={1}>
        {detail}
      </Text>
      {extra}
      <SourceBadge source={source} />
    </Group>
  )
}

/**
 * What a lane actually enforces, which has no other home in the UI.
 *
 * Guardrails, masking and OPA are not editable anywhere yet; outside this panel
 * they reduce to a Features chip, so an operator reads the YAML to learn which
 * rules run where. Every rule is marked Listener or Inherited, because the
 * merge is not a union: guardrails concatenate, masking and OPA replace, and
 * each has a spelling that means "none" rather than "inherit" (resolve.js
 * holds the rules).
 */
export default function ListenerDetails({ listener, config }) {
  const { mode, guardrails, mask, opa } = resolveListener(listener, config)
  const observing = mode === MODE_OBSERVE

  return (
    <Box bg="gray.0" p="md">
      <Stack gap="md">
        <Group gap="xs">
          {configChips(listener).map((chip) => (
            <Badge key={chip} variant="inactive" tt="none" fw={500}>
              {chip}
            </Badge>
          ))}
        </Group>

        <Divider color="gray.2" />

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
            <Stack gap={6}>
              {guardrails.map((entry, i) => (
                <Rule
                  key={`${entry.rule.name}-${i}`}
                  name={entry.rule.name}
                  source={entry.source}
                  detail={[RULE_TYPE_LABELS[entry.rule.type] ?? entry.rule.type, ruleMatcher(entry.rule)]
                    .filter(Boolean)
                    .join(' · ')}
                  // action: "defer" reports a finding instead of denying.
                  extra={
                    entry.rule.action === 'defer' && (
                      <Badge variant="light" color="amber" tt="none" fw={500}>
                        Report only
                      </Badge>
                    )
                  }
                />
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
            <Stack gap={6}>
              {mask.map((entry, i) => {
                const { rule } = entry
                const target = join(rule.columns) || join(rule.entities) || rule.entity || null
                const strategy = MASK_STRATEGY_LABELS[rule.strategy] ?? MASK_STRATEGY_LABELS.redact
                // keep_last only means anything to the partial strategy; its default is 4.
                const keepLast = rule.strategy === 'partial' ? `keep last ${rule.keep_last ?? 4}` : null
                return (
                  <Rule
                    key={`${rule.name}-${i}`}
                    name={rule.name}
                    source={entry.source}
                    detail={[target, strategy, keepLast].filter(Boolean).join(' · ')}
                  />
                )
              })}
            </Stack>
          )}
        </Section>

        <Divider color="gray.2" />

        <Section title="OPA">
          {opa ? (
            <Text size="xs" c="dimmed">
              {[opa.url, opa.fail_open ? 'allows on failure' : 'denies on failure', opa.gate && 'gates the analyzer']
                .filter(Boolean)
                .join(' · ')}
            </Text>
          ) : (
            <Text size="xs" c="dimmed">
              Not used.
            </Text>
          )}
        </Section>
      </Stack>
    </Box>
  )
}
