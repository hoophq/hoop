import { Divider, Group, Stack, Text } from '@mantine/core'
import Accordion from '@/components/Accordion'
import Badge from '@/components/Badge'
import { featureList, resolveOPA } from '../features'
import { MODE_OBSERVE, SOURCE_DISTRIBUTED, SOURCE_LISTENER } from '../resolve'
import classes from './FeatureAccordions.module.css'

const SOURCE_LABELS = {
  [SOURCE_LISTENER]: { label: 'Listener', color: 'indigo' },
  [SOURCE_DISTRIBUTED]: { label: 'Control plane', color: 'sky' },
}

// Every rule says where it came from, because the merge is not a union:
// guardrails concatenate, masking replaces, and each has a spelling that
// means "none" rather than "inherit" (resolve.js holds the rules).
function SourceBadge({ source }) {
  const { label, color } = SOURCE_LABELS[source] ?? { label: 'Inherited', color: 'gray' }
  return (
    <Badge tag variant="light" color={color}>
      {label}
    </Badge>
  )
}

function RuleRow({ rule }) {
  return (
    <Group gap="sm" align="baseline" wrap="nowrap" py="xs">
      <Text size="xs" fw={700}>
        {rule.name || 'Unnamed rule'}
      </Text>
      <Text size="xs" flex={1}>
        {rule.detail}
      </Text>
      {rule.flag && (
        <Badge tag variant="warning">
          {rule.flag}
        </Badge>
      )}
      <SourceBadge source={rule.source} />
    </Group>
  )
}

/**
 * Without a `listener` it reads the sidecar's defaults, the top-level blocks
 * every lane inherits; with one, what that lane resolves to. Both include the
 * rules the control plane distributes, which are never in the stored document.
 */
export default function FeatureAccordions({ listener = null, config, boundRules }) {
  const features = featureList(listener, config, boundRules)
  const opa = resolveOPA(listener, config)

  return (
    <Stack gap="sm">
      <Accordion
        variant="filled"
        multiple
        classNames={{ item: classes.item, control: classes.control, panel: classes.panel, content: classes.content }}
      >
        {features.map((feature) => {
          const Icon = feature.icon
          const observing = feature.mode === MODE_OBSERVE
          return (
            <Accordion.Item key={feature.key} value={feature.key} data-feature={feature.key}>
              <Accordion.Control
                icon={
                  <Text span c={`${feature.color}.6`} lh={1}>
                    <Icon size={16} aria-hidden="true" />
                  </Text>
                }
              >
                <Group justify="space-between" wrap="nowrap" pr="sm">
                  <Text size="xs" fw={700}>
                    {feature.label}
                  </Text>
                  <Text size="xs" fw={500}>
                    {feature.summary}
                  </Text>
                </Group>
              </Accordion.Control>
              <Accordion.Panel>
                <Stack gap={4}>
                  <Group gap="sm" align="center">
                    <Text size="xs" fw={700}>
                      Rules
                    </Text>
                    {observing && (
                      <Badge tag variant="warning">
                        Observe
                      </Badge>
                    )}
                  </Group>
                  {feature.rules.length === 0 ? (
                    <Text size="xs" c="dimmed">
                      {observing ? 'No rules. Nothing is evaluated.' : feature.empty}
                    </Text>
                  ) : (
                    <Stack gap={0}>
                      {feature.rules.map((rule, i) => (
                        <div key={rule.id}>
                          {i > 0 && <Divider color="gray.2" />}
                          <RuleRow rule={rule} />
                        </div>
                      ))}
                    </Stack>
                  )}
                </Stack>
              </Accordion.Panel>
            </Accordion.Item>
          )
        })}
      </Accordion>
      {/* Not a feature chip, but the one other thing a lane enforces. */}
      {opa && (
        <Text size="sm" c="dimmed">
          {[`OPA ${opa.url}`, opa.fail_open ? 'allows on failure' : 'denies on failure', opa.gate && 'gates the analyzer']
            .filter(Boolean)
            .join(' · ')}
        </Text>
      )}
    </Stack>
  )
}
