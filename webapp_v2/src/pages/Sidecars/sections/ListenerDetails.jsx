import { Box, Divider, Group, Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import { guardrailSummary, maskSummary } from '@/pages/sidecarRuleRows'
import {
  MODE_OBSERVE,
  SOURCE_DISTRIBUTED,
  SOURCE_LISTENER,
  analyzerOverriddenBy,
  maskReplacedBy,
  resolveListener,
} from '../resolve'
import { supportsGRPCBlock, supportsHTTPBlock } from '../listeners'

// What a rule matches and what it rewrites are described in
// @/pages/sidecarRuleRows, next to the rule lists that describe the same rules.
// One reader, so a row here and a row there cannot disagree about one rule.

// LaneAnalyzerConfig.HighRisk/MediumRisk/LowRisk (sidecar/daemon/analyzer.go).
// An unset level allows, so a level nobody named carries no information and is
// left out rather than printed as "low allows" three times.
const RISK_ACTIONS = {
  allow: 'allows',
  warn: 'warns',
  block: 'blocks',
  defer: 'reports',
}

const join = (v) => (Array.isArray(v) && v.length > 0 ? v.join(', ') : null)

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
  if (supportsHTTPBlock(listener.protocol) && listener.http?.capture_body) chips.push({ label: 'Body captured' })
  if (supportsGRPCBlock(listener.protocol) && listener.grpc?.capture_payload) chips.push({ label: 'Payload captured' })
  // Not a neutral fact, and it lands right after "Upstream TLS", which reads as
  // reassurance. A lane that accepts any upstream certificate has to look
  // different from one that checks, because scanning this strip for exactly
  // that is what the expanded row is for.
  if (listener.upstream_tls?.insecure_skip_verify) {
    chips.push({ label: 'Verification off', variant: 'warning' })
  }
  return chips
}

function Section({ title, right, note, children }) {
  return (
    <Stack gap="xs">
      <Group gap="sm" align="baseline">
        <Text size="sm" fw={600}>
          {title}
        </Text>
        {right}
      </Group>
      {note && (
        <Text size="xs" c="dimmed">
          {note}
        </Text>
      )}
      {children}
    </Stack>
  )
}

// Where a rule came from, in the words the rest of the app uses. The two
// document origins both say "Config file" because that is the only place a
// reader can go to change them: the control plane stores the document, but an
// operator authored it in a file and the form here does not render these
// sections at all. The distinction that survives is which SCOPE it was written
// at, because a default applies to every lane and a listener's own does not.
const SOURCE_LABELS = {
  [SOURCE_LISTENER]: { label: 'Config file', color: 'indigo' },
  [SOURCE_DISTRIBUTED]: { label: 'Control plane', color: 'sky' },
}

const INHERITED_LABEL = { label: 'Config file · default', color: 'gray' }

// Said once per section that has one, rather than on every row.
const CONFIG_FILE_NOTE = 'Rules from the config file are not editable in the control plane.'

const hasConfigFileRule = (entries) => entries.some((e) => e.source !== SOURCE_DISTRIBUTED)

function SourceBadge({ source }) {
  const { label, color } = SOURCE_LABELS[source] ?? INHERITED_LABEL
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

// `retired` is a rule the served document does not run: the fields stay
// readable, dimmed, because the operator still has to find and fix the file
// that carries them, and hiding the row would leave them wondering where it
// went.
function Rule({ name, detail, source, extra, retired }) {
  return (
    <Group gap="sm" align="baseline" wrap="nowrap">
      <Text size="xs" fw={600} c={retired ? 'dimmed' : undefined} td={retired ? 'line-through' : undefined}>
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

  // The three sections do NOT combine the same way, and only guardrails add
  // up. Drawing all three as one list was this panel saying every rule on
  // screen runs, which for masking is the opposite of the truth.
  const maskRetiredBy = maskReplacedBy(boundRules, listener?.name)
  const analyzerDrivenBy = analyzerOverriddenBy(boundRules, listener?.name)
  const maskRetired = maskRetiredBy.length > 0 && mask.length > 0

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

        {/* Guardrails are the one section that adds up: composition appends
            the control plane's rules to the lane's own (appendGuardrails),
            and the daemon then concatenates the inherited defaults after
            those. Every row here really is evaluated. */}
        <Section
          title="Guardrails"
          right={
            <Badge tag variant={observing ? 'warning' : 'inactive'}>
              {observing ? 'Observe' : 'Enforce'}
            </Badge>
          }
          note={hasConfigFileRule(guardrails) ? CONFIG_FILE_NOTE : undefined}
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
                  detail={guardrailSummary(entry.rule)}
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

        {/* Masking REPLACES. Composition writes the lane's whole `mask` block
            from the bound rules (foldSidecarRules), and a present block
            overrides the inherited one — so a control plane rule retires the
            config file's, defaults included, rather than joining them. */}
        <Section
          title="Masking"
          note={
            maskRetired
              ? `Replaced by ${maskRetiredBy.join(', ')}. A listener's mask rules are not merged: the control plane's replace the config file's.`
              : hasConfigFileRule(mask)
                ? CONFIG_FILE_NOTE
                : undefined
          }
        >
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
                return (
                  <Rule
                    key={`${rule.name}-${i}`}
                    name={rule.name}
                    source={entry.source}
                    retired={maskRetired}
                    detail={maskSummary(rule)}
                    extra={
                      maskRetired && (
                        <Badge tag variant="warning">
                          Not applied
                        </Badge>
                      )
                    }
                  />
                )
              })}
            </Stack>
          )}
        </Section>

        <Divider color="gray.2" />

        {/* The analyzer OVERRIDES, field by field. mergeAnalyzerBlock gives
            the rule the trigger, the three risk actions, the prompt and the
            message, and leaves the config file `send`, `fail_open` and the
            cost bounds. Neither side alone says what runs, so printing the
            block's own trigger under a bound rule states the opposite. */}
        <Section
          title="AI Analyzer"
          note={
            analyzerDrivenBy.length > 0 && analyzer.block
              ? `What is classified and what each verdict does come from ${analyzerDrivenBy.join(', ')}. The config file keeps what leaves the process, what happens on a provider outage, and the cost limits.`
              : undefined
          }
        >
          {analyzer.on || sentAnalyzer.length > 0 ? (
            <Stack gap={6}>
              {sentAnalyzer.map((r) => (
                <Rule key={`cp-${r.name}`} name={r.name} source={r.source} />
              ))}
              {/* The block is read off the listener, so it is always the
                  lane's own and a Listener badge beside it would say nothing.
                  Only the deprecated rules below can be inherited. */}
              {analyzer.block && analyzerDrivenBy.length === 0 && (
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
