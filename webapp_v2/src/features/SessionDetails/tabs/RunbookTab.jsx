import { Stack, Text } from '@mantine/core'
import { BookUp2 } from 'lucide-react'
import { parseParameters } from '@/stores/useSessionDetailsStore'
import InfoRow from '../components/InfoRow'

// Figma "Tabs | Runbook": the runbook file, then one row per parameter.
export default function RunbookTab({ session }) {
  const parameters = parseParameters(session.labels?.runbookParameters)
  return (
    <Stack gap="md">
      <InfoRow icon={BookUp2} label="template">
        <Text size="sm">{session.labels.runbookFile}</Text>
      </InfoRow>
      {Object.entries(parameters).map(([key, value]) => (
        <InfoRow key={key} icon={BookUp2} label={key}>
          <Text size="sm">{String(value)}</Text>
        </InfoRow>
      ))}
    </Stack>
  )
}
