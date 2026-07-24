import { useEffect } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Group, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import Tabs from '@/components/Tabs'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useUserStore } from '@/stores/useUserStore'
import { useJiraTemplatesStore } from './store'
import TemplatesTab from './TemplatesTab'
import ConfigurationTab from './ConfigurationTab'

export default function JiraTemplates() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  const list = useJiraTemplatesStore((s) => s.list)
  const listStatus = useJiraTemplatesStore((s) => s.listStatus)
  const integration = useJiraTemplatesStore((s) => s.integration)
  const integrationStatus = useJiraTemplatesStore((s) => s.integrationStatus)
  const fetchList = useJiraTemplatesStore((s) => s.fetchList)
  const fetchIntegration = useJiraTemplatesStore((s) => s.fetchIntegration)

  // ?tab=configuration deep-links to the integration form (used by the
  // "Integrations > Jira" sidebar entry); any other value means templates.
  const tab = searchParams.get('tab') === 'configuration' ? 'configuration' : 'templates'
  const setTab = (value) =>
    setSearchParams(value === 'configuration' ? { tab: value } : {}, {
      replace: true,
    })

  useEffect(() => {
    fetchList()
    fetchIntegration()
  }, [fetchList, fetchIntegration])

  const loading =
    listStatus === 'loading' ||
    listStatus === 'idle' ||
    integrationStatus === 'loading' ||
    integrationStatus === 'idle'
  const showLoader = useMinDelay(loading, 500)

  if (showLoader) {
    return <PageLoader h={400} />
  }

  if (listStatus === 'error' || integrationStatus === 'error') {
    return <Text c="red">Failed to load Jira templates.</Text>
  }

  const atFreeLimit = isFreeLicense && list.length >= 1
  const showCreate = Boolean(integration) && list.length > 0

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>Jira Templates</Title>
          <Text size="md" c="dimmed">
            Optimize and automate workflows with Jira integration.
          </Text>
        </Stack>
        {showCreate && (
          <Button
            onClick={() => navigate('/jira-templates/new')}
            disabled={atFreeLimit}
          >
            Create new
          </Button>
        )}
      </Group>

      <Tabs value={tab} onChange={setTab}>
        <Tabs.List aria-label="Jira Templates tabs">
          <Tabs.Tab value="templates">Templates</Tabs.Tab>
          <Tabs.Tab value="configuration">Configuration</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="templates" pt="md">
          <TemplatesTab onGoConfiguration={() => setTab('configuration')} />
        </Tabs.Panel>
        <Tabs.Panel value="configuration" pt="md">
          <ConfigurationTab />
        </Tabs.Panel>
      </Tabs>
    </Stack>
  )
}
