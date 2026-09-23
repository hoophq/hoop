import { Group, List, Stack, Text } from '@mantine/core'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Modal from '@/components/Modal'

const KINDS = [
  ['guardrail', 'guardrail'],
  ['datamasking', 'data masking'],
  ['analyzer', 'AI analyzer'],
]

// The rules bound to this sidecar, counted once per rule and kind.
function countRules(boundRules) {
  const seen = new Set(boundRules.map((b) => `${b.kind}:${b.rule_name}`))
  return KINDS.map(([kind, label]) => [label, [...seen].filter((k) => k.startsWith(`${kind}:`)).length]).filter(
    ([, n]) => n > 0,
  )
}

// Which side owns a sidecar's configuration. To the configuration file, the
// gateway deletes the control-plane rules bound only to this sidecar. Back to
// the control plane, the sidecar imports its configuration file again.
export default function SidecarSourceModal({ opened, toConfigFile, boundRules, onClose, onConfirm, loading }) {
  const counts = countRules(boundRules)
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={toConfigFile ? 'Use the sidecar configuration file?' : 'Let the control plane own the configuration?'}
      size="md"
    >
      <Stack>
        {toConfigFile ? (
          <>
            <Alert color="red" variant="light" radius="md" icon={<TriangleAlert size={16} />}>
              <Stack gap={4}>
                <Text size="sm" fw={600}>
                  This deletes control-plane rules.
                </Text>
                {counts.length > 0 ? (
                  <>
                    <Text size="sm">The rules bound to this sidecar are removed from it:</Text>
                    <List size="sm">
                      {counts.map(([label, n]) => (
                        <List.Item key={label}>{`${n} ${label} rule${n === 1 ? '' : 's'}`}</List.Item>
                      ))}
                    </List>
                    <Text size="sm">
                      A rule used only by this sidecar is deleted. A rule that other sidecars also use stays for them.
                    </Text>
                  </>
                ) : (
                  <Text size="sm">No control-plane rule is bound to this sidecar now.</Text>
                )}
              </Stack>
            </Alert>
            <Text size="sm">
              The sidecar will use only the rules in its configuration file. The control plane sends only its license
              and cannot manage its features.
            </Text>
          </>
        ) : (
          <Stack gap={4}>
            <Text size="sm">
              The sidecar sends its configuration file to the control plane on its next check-in. Each rule in the file
              becomes a rule in Guardrails, Data Masking and AI Session Analyzer.
            </Text>
            <Text size="sm" c="dimmed">
              An older sidecar sends its file when it restarts.
            </Text>
          </Stack>
        )}
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button color={toConfigFile ? 'red' : undefined} onClick={onConfirm} loading={loading}>
            {toConfigFile ? 'Delete rules and use file' : 'Use the control plane'}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
