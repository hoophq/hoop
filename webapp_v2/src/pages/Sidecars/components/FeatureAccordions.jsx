import { Divider, Group, Stack, Text } from '@mantine/core'
import { CirclePlus, SquarePen } from 'lucide-react'
import Accordion from '@/components/Accordion'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import { SOURCE_SIDECAR, featureList, opaSummary, overridesOPA, resolveOPA } from '../features'
import { MODE_OBSERVE, SOURCE_DISTRIBUTED, SOURCE_LISTENER } from '../resolve'
import classes from './FeatureAccordions.module.css'

const SOURCE_LABELS = {
  [SOURCE_LISTENER]: { label: 'Listener', color: 'indigo' },
  [SOURCE_DISTRIBUTED]: { label: 'Control plane', color: 'sky' },
  [SOURCE_SIDECAR]: { label: 'Sidecar', color: 'gray' },
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

function RuleRow({ rule, onEdit }) {
  return (
    <Group gap="sm" align="center" wrap="nowrap" py="xs">
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
      {onEdit && (
        <Button variant="subtle" size="compact-xs" leftSection={<SquarePen size={14} />} onClick={onEdit}>
          Edit
        </Button>
      )}
    </Group>
  )
}

/**
 * Without a `listener` it reads the sidecar's defaults, the top-level blocks
 * every lane inherits; with one, what that lane resolves to. Both include the
 * rules the control plane distributes, which are never in the stored document.
 * `actions` adds Add per feature and Edit on distributed rules; rules embedded
 * in the document have no form here.
 */
export default function FeatureAccordions({ listener = null, config, boundRules, flush = false, actions }) {
  const features = featureList(listener, config, boundRules)
  // The sidecar's endpoint is a Global settings row; a lane speaks only on override.
  const ownOPA = overridesOPA(listener)
  const opa = ownOPA ? resolveOPA(listener, config) : null

  return (
    <Stack gap="sm">
      <Accordion
        variant="filled"
        radius={flush ? 0 : 'md'}
        multiple
        classNames={{ item: classes.item, control: classes.control, label: classes.label, panel: classes.panel, content: classes.content }}
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
                  <Group justify="space-between" align="center" wrap="nowrap">
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
                    {actions && (
                      <Button
                        variant="subtle"
                        size="compact-xs"
                        leftSection={<CirclePlus size={14} />}
                        onClick={() => actions.onAdd(feature.key)}
                      >
                        Add
                      </Button>
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
                          <RuleRow
                            rule={rule}
                            onEdit={
                              actions && rule.source === SOURCE_DISTRIBUTED
                                ? () => actions.onEdit(feature.key, rule)
                                : undefined
                            }
                          />
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
      {ownOPA && (
        <Text size="sm" c="dimmed">
          {opa?.url ? `OPA on this listener: ${opaSummary(opa)}` : 'OPA is off on this listener.'}
        </Text>
      )}
    </Stack>
  )
}
