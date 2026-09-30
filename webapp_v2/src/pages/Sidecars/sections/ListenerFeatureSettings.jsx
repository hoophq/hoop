import { Group, Stack, Text } from '@mantine/core'
import { Info } from 'lucide-react'
import Accordion from '@/components/Accordion'
import Callout from '../components/Callout'
import FeatureAccordions from '../components/FeatureAccordions'

/**
 * What the listener runs for each feature. A new one has no name yet, so it
 * resolves to the sidecar's defaults and nothing the control plane distributes;
 * an existing one shows what it resolves to today. Editing the rules from here
 * is EVL-371.
 */
export default function ListenerFeatureSettings({ sidecar, listener }) {
  const isNew = listener == null
  return (
    <Accordion>
      <Accordion.Item value="features">
        <Accordion.Control>
          <Group justify="space-between" wrap="nowrap" pr="sm">
            <Text fw={600}>Feature settings</Text>
            <Text size="sm" c="dimmed">
              Optional
            </Text>
          </Group>
        </Accordion.Control>
        <Accordion.Panel>
          <Stack gap="md" pt="xs">
            <FeatureAccordions
              listener={isNew ? {} : listener}
              config={sidecar.configuration}
              boundRules={sidecar.bound_rules}
            />
            {isNew && (
              <Callout icon={Info} color="indigo.0">
                <Text size="sm">
                  This listener inherits the global feature configuration. You can override it per listener after
                  creation. Restart the sidecar to load the new listener configuration.
                </Text>
              </Callout>
            )}
          </Stack>
        </Accordion.Panel>
      </Accordion.Item>
    </Accordion>
  )
}
