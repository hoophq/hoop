import { useState } from 'react'
import { Anchor, Box, Grid, Group, Stack, Text, Title } from '@mantine/core'
import { Info } from 'lucide-react'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import Switch from '@/components/Switch'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import { showSnackbar } from '@/utils/snackbar'
import { useUserStore } from '@/stores/useUserStore'
import { useJiraTemplatesStore } from './store'

function SectionRow({ title, description, children }) {
  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Title order={4} fw={500}>
            {title}
          </Title>
          <Text size="sm" c="dimmed">
            {description}
          </Text>
        </Stack>
      </Grid.Col>
      <Grid.Col span={5}>{children}</Grid.Col>
    </Grid>
  )
}

const FREE_LICENSE_INFO_MESSAGE =
  'Organizations with Free plan have limited automation. Upgrade to Enterprise to have unlimited access to the Jira integration.'
const ATLASSIAN_TOKEN_DOCS_URL =
  'https://support.atlassian.com/atlassian-account/docs/manage-api-tokens-for-your-atlassian-account/'

// Remounted via `key` when the loaded integration changes, so state derives
// from `integration` with lazy useState initializers instead of a prefill effect.
function ConfigurationForm({ integration }) {
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const submitting = useJiraTemplatesStore((s) => s.submitting)
  const saveIntegration = useJiraTemplatesStore((s) => s.saveIntegration)

  const isCreate = !integration
  const [enabled, setEnabled] = useState(() => integration?.status === 'enabled')
  const [url, setUrl] = useState(() => integration?.url ?? '')
  const [userEmail, setUserEmail] = useState(() => integration?.user ?? '')
  // The API requires api_token on PUT as well; admins receive the real token
  // on GET, so prefilling keeps updates valid without retyping the secret.
  const [apiToken, setApiToken] = useState(() => integration?.api_token ?? '')

  const handleSubmit = async (event) => {
    event.preventDefault()
    const { ok, error } = await saveIntegration({
      url,
      user: userEmail,
      api_token: apiToken,
      status: enabled ? 'enabled' : 'disabled',
    })
    if (ok) {
      showSnackbar({
        level: 'success',
        text: isCreate ? 'Jira integration created!' : 'Jira integration updated!',
      })
    } else {
      showSnackbar({
        level: 'error',
        text: error?.response?.data?.message || 'Failed to save Jira integration.',
      })
    }
  }

  return (
    <Stack gap="xl">
      {isFreeLicense && (
        <FreeLicenseCallout message={FREE_LICENSE_INFO_MESSAGE} variant="info" />
      )}

      <SectionRow
        title="Configure integration"
        description="Boost productivity by linking your resource roles with Jira."
      >
        <form onSubmit={handleSubmit}>
          <Stack gap="lg">
            <Switch
              label="Enable integration"
              checked={enabled}
              onChange={(e) => setEnabled(e.currentTarget.checked)}
            />
            <TextInput
              label="Jira Instance URL"
              placeholder="https://your-domain.atlassian.net"
              value={url}
              onChange={(e) => setUrl(e.currentTarget.value)}
              disabled={!enabled}
              required
            />
            <TextInput
              label="User Email"
              placeholder="name@company.com"
              value={userEmail}
              onChange={(e) => setUserEmail(e.currentTarget.value)}
              disabled={!enabled}
              required
            />
            <Stack gap="xs">
              <PasswordInput
                label="User API token"
                placeholder="lXtpBPQvBvSycVYDGo7S8k2N12KE1dMcdyastG"
                value={apiToken}
                onChange={(e) => setApiToken(e.currentTarget.value)}
                disabled={!enabled}
                required
              />
              <Group gap="xs" align="center" wrap="nowrap">
                <Info size={16} color="var(--mantine-color-dimmed)" />
                <Text size="sm" c="dimmed">
                  {'For more information about how to find your User API token, '}
                  <Anchor
                    href={ATLASSIAN_TOKEN_DOCS_URL}
                    target="_blank"
                    rel="noopener noreferrer"
                    size="sm"
                  >
                    click here
                  </Anchor>
                  {'.'}
                </Text>
              </Group>
            </Stack>
            <Box>
              <Button type="submit" loading={submitting}>
                Confirm
              </Button>
            </Box>
          </Stack>
        </form>
      </SectionRow>
    </Stack>
  )
}

export default function ConfigurationTab() {
  const integration = useJiraTemplatesStore((s) => s.integration)
  return (
    <ConfigurationForm key={integration?.id ?? 'new'} integration={integration} />
  )
}
