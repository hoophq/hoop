import { useState } from 'react'
import { Box, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { CircleHelp, Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Select from '@/components/Select'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import Tooltip from '@/components/Tooltip'
import { showSnackbar } from '@/utils/snackbar'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'
import AgentSelector from '@/pages/Roles/Configure/sections/credentials/shared/AgentSelector'
import FederationTestModal from '@/pages/Roles/Configure/sections/FederationTestModal'
import {
  adminCredentialsForTest,
  FEDERATION_PROVIDERS,
  hasUsableStoredCredentials,
  OUTPUT_ENV_VARS,
} from '@/pages/Roles/Configure/utils/federation'

const PROVIDER_OPTIONS = [
  { value: FEDERATION_PROVIDERS.GCP_IAM, label: 'GCP IAM (service account impersonation)' },
  { value: FEDERATION_PROVIDERS.GCP_OAUTH, label: 'GCP OAuth (per-user Google account)' },
]

const FALLBACK_OPTIONS = [
  { value: 'deny', label: 'Deny the session' },
  { value: 'static', label: "Use the connection's static credentials" },
]

function Section({ title, help, children }) {
  return (
    <Stack gap="md">
      <Group gap={6}>
        <Title order={5} fw={600}>{title}</Title>
        <Tooltip label={help} multiline w={320}>
          <Box component="span" display="inline-flex" c="dimmed">
            <CircleHelp size={14} />
          </Box>
        </Tooltip>
      </Group>
      {children}
    </Stack>
  )
}

function AdminCredentialsField({ oauth }) {
  const form = useConfigureRoleStore((s) => s.federation.form)
  const stored = useConfigureRoleStore((s) =>
    hasUsableStoredCredentials(s.federation.config, s.federation.form),
  )
  const editing = useConfigureRoleStore((s) => s.federation.credentialsEditing)
  const setFederationForm = useConfigureRoleStore((s) => s.setFederationForm)
  const setEditing = useConfigureRoleStore((s) => s.setFederationCredentialsEditing)
  const label = oauth ? 'OAuth client credentials (JSON)' : 'Service account JSON'

  if (stored && !editing && !form.admin_credentials_json) {
    return (
      <Stack gap="xs">
        <Textarea label={label} value="••••••••••••••••••••••" readOnly disabled />
        <Group>
          <Button variant="subtle" size="compact-sm" onClick={() => setEditing(true)}>
            Replace credentials
          </Button>
        </Group>
      </Stack>
    )
  }

  return (
    <Textarea
      label={label}
      required={!stored}
      minRows={7}
      maxRows={14}
      placeholder={
        oauth
          ? '{\n  "client_id": "...apps.googleusercontent.com",\n  "client_secret": "..."\n}'
          : '{\n  "type": "service_account",\n  ...\n}'
      }
      value={form.admin_credentials_json}
      onChange={(e) => setFederationForm({ admin_credentials_json: e.currentTarget.value })}
    />
  )
}

function IdentityMapping() {
  const form = useConfigureRoleStore((s) => s.federation.form)
  const setFederationForm = useConfigureRoleStore((s) => s.setFederationForm)
  return (
    <Stack gap="md">
      <TextInput
        label="Source attribute (Hoop)"
        description="JSONPath into the Hoop session context."
        placeholder="$.user.email"
        required
        value={form.identity_source_attribute}
        onChange={(e) => setFederationForm({ identity_source_attribute: e.currentTarget.value })}
      />
      <TextInput
        label="Target principal template (GCP)"
        description="Use a literal email or interpolate from the source. Example: analyst-{user.email}@your-project.iam.gserviceaccount.com."
        placeholder="{user.email}"
        required
        value={form.identity_target_template}
        onChange={(e) => setFederationForm({ identity_target_template: e.currentTarget.value })}
      />
      <Select
        label="Fallback method"
        description={
          "What happens when a user's identity can't be mapped to a cloud principal at session start. " +
          '"Deny the session" blocks access; "Use the connection\'s static credentials" runs the session on the credentials stored on the connection.'
        }
        data={FALLBACK_OPTIONS}
        value={form.fallback_policy}
        onChange={(v) => v && setFederationForm({ fallback_policy: v })}
        allowDeselect={false}
      />
    </Stack>
  )
}

function OAuthConsent() {
  const savedAsOAuth = useConfigureRoleStore(
    (s) => s.federation.config?.builtin_provider === FEDERATION_PROVIDERS.GCP_OAUTH,
  )
  const status = useConfigureRoleStore((s) => s.federation.oauthStatus)
  const startFederationOAuth = useConfigureRoleStore((s) => s.startFederationOAuth)
  const disconnectFederationOAuth = useConfigureRoleStore((s) => s.disconnectFederationOAuth)
  const [connecting, setConnecting] = useState(false)
  const [disconnecting, setDisconnecting] = useState(false)

  const connect = async () => {
    setConnecting(true)
    try {
      const url = await startFederationOAuth()
      if (url) {
        window.location.assign(url)
        return
      }
      setConnecting(false)
    } catch (err) {
      setConnecting(false)
      showSnackbar({
        level: 'error',
        text: 'Failed to start Google authorization',
        description: err?.response?.data?.message,
      })
    }
  }

  const disconnect = async () => {
    setDisconnecting(true)
    try {
      await disconnectFederationOAuth()
      showSnackbar({ level: 'success', text: 'Google account disconnected' })
    } catch (err) {
      if (err?.response?.status === 404) {
        showSnackbar({ level: 'info', text: 'No Google account was connected' })
      } else {
        showSnackbar({
          level: 'error',
          text: 'Failed to disconnect Google account',
          description: err?.response?.data?.message,
        })
      }
    } finally {
      setDisconnecting(false)
    }
  }

  if (!savedAsOAuth) {
    return (
      <Alert color="blue" variant="light" icon={<Info size={16} />}>
        Save the role with GCP OAuth first. Then each user — including you — connects their own
        Google account from this screen before running a session.
      </Alert>
    )
  }

  return (
    <Stack gap="sm">
      <Text size="sm">
        {status?.connected
          ? 'Connected as ' + (status.google_email || 'a Google account')
          : 'Your Google account is not connected to this role.'}
      </Text>
      <Group gap="sm">
        <Button onClick={connect} loading={connecting}>
          Connect Google account
        </Button>
        <Button
          variant="default"
          onClick={disconnect}
          loading={disconnecting}
          disabled={status?.connected === false}
        >
          Disconnect
        </Button>
      </Group>
      <Alert color="gray" variant="light" icon={<Info size={16} />}>
        {"This connects the Google account of the currently signed-in user. Every user who runs this role must connect their own account once; the consented Google identity becomes the session's audited principal."}
      </Alert>
    </Stack>
  )
}

function OutputPreview({ provider }) {
  return (
    <Stack gap="xs">
      <Paper withBorder p="md" bg="gray.0">
        <Stack gap={4}>
          {OUTPUT_ENV_VARS[provider].map((name) => (
            <Text key={name} size="sm" ff="monospace">
              <Text span inherit c="indigo.7">{name}</Text>
              <Text span inherit c="dimmed">{'=<issued at session start>'}</Text>
            </Text>
          ))}
        </Stack>
      </Paper>
      <Text size="xs" c="dimmed">
        Hoop guarantees these names exist for every successful session. Your queries can rely on
        them the same way they would with statically configured credentials.
      </Text>
    </Stack>
  )
}

function testBlocker(form, { stored, testCredentials }, agentId) {
  if (!testCredentials) {
    return stored
      ? 'Paste the service account JSON to test: the stored key cannot be read here.'
      : 'Add service account credentials to enable testing.'
  }
  if (!form.extra_config.project_id.trim()) return 'Set the GCP Project ID to enable testing.'
  if (!form.identity_source_attribute.trim() || !form.identity_target_template.trim()) {
    return 'Set the identity mapping to enable testing.'
  }
  if (!agentId) return 'Select an agent to enable testing.'
  return null
}

export default function FederationSection() {
  const form = useConfigureRoleStore((s) => s.federation.form)
  const stored = useConfigureRoleStore((s) =>
    hasUsableStoredCredentials(s.federation.config, s.federation.form),
  )
  const secrets = useConfigureRoleStore((s) => s.connection?.secret)
  const hideRoleInfo = useConfigureRoleStore((s) => s.hideRoleInfo)
  const agentId = useConfigureRoleStore((s) => s.drafts.agent_id)
  const setFederationProvider = useConfigureRoleStore((s) => s.setFederationProvider)
  const setFederationProjectId = useConfigureRoleStore((s) => s.setFederationProjectId)
  const [testOpened, { open: openTest, close: closeTest }] = useDisclosure(false)

  const oauth = form.builtin_provider === FEDERATION_PROVIDERS.GCP_OAUTH
  const testCredentials = adminCredentialsForTest(form, secrets, hideRoleInfo)
  const blocker = testBlocker(form, { stored, testCredentials }, agentId)

  return (
    <Stack gap="xl">
      <Group justify="space-between">
        <Title order={4}>Federation setup</Title>
        {!oauth && (
          <Tooltip label={blocker || 'Run a dry-run test as a specific Hoop user'}>
            <Box component="span" display="inline-block">
              <Button variant="light" onClick={openTest} disabled={Boolean(blocker)}>
                Test as user
              </Button>
            </Box>
          </Tooltip>
        )}
      </Group>

      <Select
        label="Federation provider"
        description="GCP IAM impersonates a service account at session time. GCP OAuth mints session tokens from each user's own Google account — no service accounts, but every user must connect their account once."
        data={PROVIDER_OPTIONS}
        value={form.builtin_provider}
        onChange={(v) => v && setFederationProvider(v)}
        allowDeselect={false}
      />

      <Section
        title={oauth ? 'OAuth application credentials' : 'Admin role credentials'}
        help={
          oauth
            ? 'The OAuth client (client_id + client_secret) the gateway uses to run the Google consent flow and exchange per-user tokens.'
            : 'The service account that the hook impersonates with. Must hold roles/iam.serviceAccountTokenCreator on the principals it issues credentials for.'
        }
      >
        <AdminCredentialsField oauth={oauth} />
        <Alert color="gray" variant="light" icon={<Info size={16} />}>
          {oauth
            ? 'These OAuth client credentials identify your Google OAuth app. Each user connects their own Google account; the client secret is only used by the gateway to exchange tokens and never reaches end-user sessions.'
            : 'This service account never touches end-user sessions directly. It is only used by the federation script to mint short-lived credentials for the mapped identity.'}
        </Alert>
        <TextInput
          label="GCP Project ID"
          placeholder="e.g. my-gcp-project"
          required
          value={form.extra_config.project_id}
          onChange={(e) => setFederationProjectId(e.currentTarget.value)}
        />
      </Section>

      {oauth ? (
        <Section
          title="Connect your Google account"
          help="gcp_oauth runs each session as the user's own Google identity. Every user connects their Google account once per role."
        >
          <OAuthConsent />
        </Section>
      ) : (
        <Section
          title="Identity mapping"
          help="How the Hoop user's identity maps to the cloud IAM principal that gets impersonated."
        >
          <IdentityMapping />
        </Section>
      )}

      <Section
        title="Output preview"
        help="Environment variables the hook writes into the command runtime. They exist only for the duration of the session."
      >
        <OutputPreview provider={form.builtin_provider} />
      </Section>

      <AgentSelector />

      {testOpened && <FederationTestModal opened onClose={closeTest} />}
    </Stack>
  )
}
