import { useNavigate } from 'react-router-dom'
import { Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import { docsUrl } from '@/utils/docsUrl'
import SectionRow from './components/SectionRow'

// The sidecar's analyzer provider is NOT an organization setting, so this tab
// collects nothing.
//
// Three facts put it out of reach of a page like this one, and each is the
// reason a field is absent rather than empty:
//
//   - The credential is a PATH on the sidecar's filesystem
//     (AnalyzerConfig.CredentialsFile). There is no inline field to send a key
//     to, and adding one would be a new key in the served document that every
//     older sidecar refuses outright.
//   - The section is per SIDECAR. One relay can run Vertex against a GCP
//     project while another runs Anthropic, and an organization-wide provider
//     cannot say that.
//   - It is baseline, not rules. Changing it restarts the process, where
//     everything the rule forms write swaps into a running one.
//
// The gateway's sibling of this file saves an api_key into private.ai_providers,
// which only gateway/aianalyzer and the transport read. Rendering it here would
// take a provider, a model and a key from an admin and reach no sidecar with
// any of them.

const FIELDS = [
  ['provider', 'vertex, anthropic or openai — whichever the binary links'],
  ['model', 'provider-specific, e.g. claude-sonnet-4-5@20250929'],
  ['credentials_file', 'a path on the sidecar. Omit it where the platform resolves credentials'],
  ['extra', 'provider settings, such as Vertex project and region'],
  ['send', 'raw, redacted or refuse — what leaves the process'],
  ['fail_open', 'true by default: a provider outage allows rather than stopping the database'],
  ['timeout_sec, max_input_bytes, max_calls, cache', 'the cost and latency bounds every lane inherits'],
]

export default function ControlPlaneConfigureTab() {
  const navigate = useNavigate()

  return (
    <Stack gap="xxlAlt" pb="xl">
      <SectionRow
        title="The provider lives on the sidecar"
        description="One provider serves every listener on a sidecar, and its credential is a file the sidecar reads at startup. The control plane distributes what each listener does with a verdict, not who classifies it."
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

          {/* A term and what it does, not a data table: no header, no row
              borders, nothing to sort or scan down a column. Rows, so it
              reads as the list it is and carries no table chrome. */}
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
            A listener needs its own analyzer block before a rule can reach it.
            Rules then carry the trigger, the action per risk level and the
            prompt.
          </Text>

          <Button variant="light" w="fit-content" onClick={() => navigate('/sidecars')}>
            Go to Sidecars
          </Button>
        </Stack>
      </SectionRow>
    </Stack>
  )
}
