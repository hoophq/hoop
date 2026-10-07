import { Stack, Text } from '@mantine/core'
import { ChevronDown } from 'lucide-react'
import ActionMenu from '@/components/ActionMenu'
import Button from '@/components/Button'
import { TRAFFIC_AGENT, TRAFFIC_SIDECAR } from '@/utils/ruleTraffic'

const OPTIONS = [
  { traffic: TRAFFIC_AGENT, label: 'For agent resources', description: 'Resource roles served by an agent' },
  { traffic: TRAFFIC_SIDECAR, label: 'For sidecar listeners', description: 'Listeners of your sidecars' },
]

// `blocked` maps a traffic to the reason it cannot take a new rule.
export default function NewRuleButton({ traffics, onCreate, disabled, blocked = {}, children }) {
  if (traffics.length === 1) {
    return (
      <Button onClick={() => onCreate(traffics[0])} disabled={disabled}>
        {children}
      </Button>
    )
  }
  return (
    <ActionMenu
      width={280}
      target={
        <Button disabled={disabled} rightSection={<ChevronDown size={16} />}>
          {children}
        </Button>
      }
    >
      {OPTIONS.map(({ traffic, label, description }) => (
        <ActionMenu.Item
          key={traffic}
          disabled={Boolean(blocked[traffic])}
          onClick={() => onCreate(traffic)}
        >
          <Stack gap={2}>
            <Text size="sm">{label}</Text>
            <Text size="xs" c="dimmed">
              {blocked[traffic] || description}
            </Text>
          </Stack>
        </ActionMenu.Item>
      ))}
    </ActionMenu>
  )
}
