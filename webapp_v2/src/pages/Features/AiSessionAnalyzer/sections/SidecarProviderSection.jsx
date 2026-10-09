import { Group, Stack, Text } from '@mantine/core'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import { docsUrl } from '@/utils/docsUrl'
import SectionRow from '../components/SectionRow'

// Collects nothing: a sidecar reads its provider credential from a file on its
// own disk, and a new key in the served document breaks older sidecars.

const FIELDS = [
  ['provider', 'vertex, anthropic or openai — whichever the binary links'],
  ['model', 'provider-specific, e.g. claude-sonnet-4-5@20250929'],
  ['credentials_file', 'a path on the sidecar. Omit it where the platform resolves credentials'],
  ['extra', 'provider settings, such as Vertex project and region'],
  ['send', 'raw, redacted or refuse — what leaves the process'],
  ['fail_open', 'true by default: a provider outage allows rather than stopping the database'],
  ['timeout_sec, max_input_bytes, max_calls, cache', 'the cost and latency bounds every lane inherits'],
]

export default function SidecarProviderSection() {
  return (
    <SectionRow
      title="Sidecar provider"
      description="The provider above analyzes agent sessions only. One provider serves every listener on a sidecar, and its credential is a file the sidecar reads at startup."
      callout={
        <DocsBtnCallOut
          text="See our docs for the analyzer section"
          href={docsUrl.sidecar.riskAnalysis}
          variant="indigo"
        />
      }
    >
      <Stack gap="lg" maw={720}>
        <Text size="sm" c="dimmed">
          Set these in the sidecar's own configuration, under a top-level
          analyzer section:
        </Text>

        <Stack gap="xs">
          {FIELDS.map(([key, what]) => (
            <Group key={key} align="flex-start" gap="md" wrap="nowrap">
              <Text size="sm" fw={600} ff="monospace" w={260} style={{ flexShrink: 0 }}>
                {key}
              </Text>
              <Text size="sm" c="dimmed">
                {what}
              </Text>
            </Group>
          ))}
        </Stack>

        <Text size="sm" c="dimmed">
          With this section in place, a rule can reach any listener of the
          sidecar. The rule carries the trigger, the action per risk level,
          the prompt and the call budget. A listener that has its own
          analyzer block keeps its limits under the rule.
        </Text>
      </Stack>
    </SectionRow>
  )
}
