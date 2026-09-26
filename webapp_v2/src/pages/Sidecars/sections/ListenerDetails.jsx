import { Box, Divider, Group, Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import { MODE_OBSERVE, SOURCE_DISTRIBUTED, SOURCE_LISTENER, resolveListener } from '../resolve'
import { listenerAccepts } from '../schema'

// The rule types of sidecar/policy a guardrail list can show. NOT the two in
// pages/Guardrails/helpers.js — those belong to the gateway's own guardrails,
// a different product with a different schema.
//
// `ai_analysis` is the ninth and is absent on purpose: resolveListener sorts it
// out of the guardrails and into the analyzer, the way the daemon does
// (splitAnalyzerRules), so it is read below by analyzerDetail instead.
const RULE_TYPE_LABELS = {
  deny_words_list: 'Deny words',
  pattern_match: 'Pattern match',
  operation: 'Operation',
  table: 'Table',
  pii: 'PII',
  http_resource: 'HTTP resource',
  http_status: 'HTTP status',
  grpc_status: 'gRPC status',
}

// LaneAnalyzerConfig.HighRisk/MediumRisk/LowRisk (sidecar/daemon/analyzer.go).
// An unset level allows, so a level nobody named carries no information and is
// left out rather than printed as "low allows" three times.
const RISK_ACTIONS = {
  allow: 'allows',
  warn: 'warns',
  block: 'blocks',
  defer: 'reports',
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
    default:
      return null
  }
}

/**
 * What the analyzer classifies, and what it does with the answer.
 *
 * Reads a lane's `analyzer` block and a DEPRECATED `ai_analysis` rule with the
 * same code, because they are the same six keys under the same names — which is
 * exactly what specFromRule asserts in sidecar/daemon/analyzer.go. Two readers
 * here would be two chances to disagree about a lane that carries both.
 */
function analyzerDetail(spec) {
  const trigger = [spec.trigger?.operations, spec.trigger?.tables, spec.trigger?.resources]
    .map(join)
    .filter(Boolean)
    .join(', ')
  const risks = ['high', 'medium', 'low']
    .filter((level) => spec[level])
    .map((level) => `${level} ${RISK_ACTIONS[spec[level]] ?? spec[level]}`)
  // A lane prompt replaces the inherited risk guidance, so two lanes with the
  // same risk map can still reach different verdicts. Nothing else on this row
  // would show that, and the text itself belongs in the form, not here.
  const prompt = spec.prompt ? 'custom prompt' : null
  // An omitted trigger is not "nothing": declaring the analyzer IS the opt-in,
  // and the lane then classifies everything it carries.
  return [trigger || 'Every statement', ...risks, prompt].filter(Boolean).join(' · ')
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
  const chips = [{ label: listener.network === 'unix' ? 'Unix socket' : 'TCP' }]
  if (listener.upstream_tls) chips.push({ label: 'Upstream TLS' })
  if (listener.downstream_tls) chips.push({ label: 'Client TLS' })
  if (listener.max_conns) chips.push({ label: `max ${listener.max_conns} conns` })
  if (listener.idle_timeout_sec) chips.push({ label: `idle ${listener.idle_timeout_sec}s` })
  if (listenerAccepts(listener.protocol, 'http') && listener.http?.capture_body) chips.push({ label: 'Body captured' })
  if (listenerAccepts(listener.protocol, 'grpc') && listener.grpc?.capture_payload) chips.push({ label: 'Payload captured' })
  // Not a neutral fact, and it lands right after "Upstream TLS", which reads as
  // reassurance. A lane that accepts any upstream certificate has to look
  // different from one that checks, because scanning this strip for exactly
  // that is what the expanded row is for.
  if (listener.upstream_tls?.insecure_skip_verify) {
    chips.push({ label: 'Verification off', variant: 'warning' })
  }
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

const SOURCE_LABELS = {
  [SOURCE_LISTENER]: { label: 'Listener', color: 'indigo' },
  [SOURCE_DISTRIBUTED]: { label: 'Control plane', color: 'sky' },
}

function SourceBadge({ source }) {
  const { label, color } = SOURCE_LABELS[source] ?? { label: 'Inherited', color: 'gray' }
  return (
    <Badge tag variant="light" color={color}>
      {label}
    </Badge>
  )
}

// The rules the control plane sends this lane, as rows the panel can render
// beside the document's own. Only the name: the binding carries no rule body,
// because a list page has no use for one and shipping every spec to render a
// row would put the fleet's whole policy on the wire.
function distributed(boundRules, listenerName, kind) {
  return (boundRules ?? [])
    .filter((b) => b.listener_name === listenerName && b.kind === kind)
    .map((b) => ({ name: b.rule_name, source: SOURCE_DISTRIBUTED }))
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
 * Guardrails, masking, the analyzer and OPA are not editable anywhere yet;
 * outside this panel they reduce to a Features chip, so an operator reads the
 * YAML to learn which rules run where. Every rule is marked Listener or
 * Inherited, because the merge is not a union: guardrails concatenate, masking
 * and OPA replace, and each has a spelling that means "none" rather than
 * "inherit" (resolve.js holds the rules).
 */
export default function ListenerDetails({ listener, config, boundRules }) {
  const { mode, guardrails, mask, opa, analyzer } = resolveListener(listener, config)
  const observing = mode === MODE_OBSERVE
  // Rendered beside the document's own rules rather than merged into them: an
  // empty section that says "everything passes" while the control plane is
  // distributing a rule to this lane is a false statement about enforcement,
  // which is the one thing this panel exists to get right.
  const sentGuardrails = distributed(boundRules, listener?.name, 'guardrail')
  const sentMask = distributed(boundRules, listener?.name, 'datamasking')
  const sentAnalyzer = distributed(boundRules, listener?.name, 'analyzer')

  return (
    <Box bg="gray.0" p="md">
      <Stack gap="md">
        <Group gap="xs">
          {configChips(listener).map((chip) => (
            <Badge key={chip.label} tag variant={chip.variant ?? 'inactive'}>
              {chip.label}
            </Badge>
          ))}
        </Group>

        <Divider color="gray.2" />

        <Section
          title="Guardrails"
          right={
            <Badge tag variant={observing ? 'warning' : 'inactive'}>
              {observing ? 'Observe' : 'Enforce'}
            </Badge>
          }
        >
          {guardrails.length === 0 && sentGuardrails.length === 0 ? (
            <Text size="xs" c="dimmed">
              {observing ? 'No rules. Nothing is evaluated.' : 'No rules. Everything passes.'}
            </Text>
          ) : (
            <Stack gap={6}>
              {sentGuardrails.map((r) => (
                <Rule key={`cp-${r.name}`} name={r.name} source={r.source} />
              ))}
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
                      <Badge tag variant="warning">
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
          {mask.length === 0 && sentMask.length === 0 ? (
            <Text size="xs" c="dimmed">
              No rules. Responses are returned unchanged.
            </Text>
          ) : (
            <Stack gap={6}>
              {sentMask.map((r) => (
                <Rule key={`cp-${r.name}`} name={r.name} source={r.source} />
              ))}
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

        <Section title="AI Analyzer">
          {analyzer.on || sentAnalyzer.length > 0 ? (
            <Stack gap={6}>
              {sentAnalyzer.map((r) => (
                <Rule key={`cp-${r.name}`} name={r.name} source={r.source} />
              ))}
              {/* The block is read off the listener, so it is always the
                  lane's own and a Listener badge beside it would say nothing.
                  Only the deprecated rules below can be inherited. */}
              {analyzer.block && (
                <Text size="xs" c="dimmed">
                  {analyzerDetail(analyzer.block)}
                </Text>
              )}
              {analyzer.deprecated.map((entry, i) => (
                <Rule
                  key={`${entry.rule.name}-${i}`}
                  name={entry.rule.name}
                  source={entry.source}
                  detail={analyzerDetail(entry.rule)}
                  // An `ai_analysis` rule still runs, and runs through the same
                  // builder the block does. It is marked because the rule form
                  // carries no per-lane overrides and the block does.
                  extra={
                    <Badge tag variant="warning">
                      Deprecated
                    </Badge>
                  }
                />
              ))}
            </Stack>
          ) : (
            <Text size="xs" c="dimmed">
              Not used. Nothing is sent to a model.
            </Text>
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
