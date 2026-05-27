import { useState, useEffect } from 'react'
import {
  Button,
  Grid,
  Group,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { BarChart3, EyeOff, ShieldOff } from 'lucide-react'
import { useMinDelay } from '@/hooks/useMinDelay'
import PageLoader from '@/components/PageLoader'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import SelectionCard from '@/components/SelectionCard'
import infrastructure from '@/services/infrastructure'
import { docsUrl } from '@/utils/docsUrl'

const ANALYTICS_OPTIONS = [
  {
    value: 'identified',
    icon: BarChart3,
    title: 'Identified',
    description:
      'Share your data with our analytics tools so we can offer onboarding, support, and product updates.',
  },
  {
    value: 'anonymous',
    icon: EyeOff,
    title: 'Anonymous',
    description:
      'Send only hashed identifiers. No personally identifiable information leaves the gateway.',
  },
  {
    value: 'disabled',
    icon: ShieldOff,
    title: 'Disabled',
    description: 'Stop all analytics events for this organization.',
  },
]

const EMPTY_FORM = {
  analyticsMode: '',
  grpcUrl: '',
  postgresPort: '',
  sshPort: '',
  rdpPort: '',
  httpPort: '',
}

function extractPort(listenAddress) {
  if (!listenAddress) return ''
  const parts = listenAddress.split(':')
  return parts[parts.length - 1] ?? ''
}

function buildMiscPayload(form) {
  const addr = (port) => (port ? `0.0.0.0:${port}` : '')
  return {
    grpc_server_url: form.grpcUrl,
    postgres_server_config: { listen_address: addr(form.postgresPort) },
    ssh_server_config: { listen_address: addr(form.sshPort) },
    rdp_server_config: { listen_address: addr(form.rdpPort) },
    http_proxy_server_config: { listen_address: addr(form.httpPort) },
  }
}

function SectionRow({ title, description, callout, children }) {
  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Title order={4}>{title}</Title>
          <Text size="sm" c="dimmed">
            {description}
          </Text>
          {callout}
        </Stack>
      </Grid.Col>
      <Grid.Col span={5}>{children}</Grid.Col>
    </Grid>
  )
}

function SettingsInfrastructure() {
  const [form, setForm] = useState(EMPTY_FORM)
  const [initialAnalyticsMode, setInitialAnalyticsMode] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const showLoader = useMinDelay(loading)

  useEffect(() => {
    Promise.all([infrastructure.get(), infrastructure.getAnalyticsMode()])
      .then(([misc, analytics]) => {
        const mode = analytics?.analytics_mode ?? ''
        setInitialAnalyticsMode(mode)
        setForm({
          analyticsMode: mode,
          grpcUrl: misc.grpc_server_url ?? '',
          postgresPort: extractPort(misc.postgres_server_config?.listen_address),
          sshPort: extractPort(misc.ssh_server_config?.listen_address),
          rdpPort: extractPort(misc.rdp_server_config?.listen_address),
          httpPort: extractPort(misc.http_proxy_server_config?.listen_address),
        })
      })
      .catch(() => {
        notifications.show({
          color: 'red',
          title: 'Error',
          message: 'Failed to load infrastructure configuration',
        })
      })
      .finally(() => setLoading(false))
  }, [])

  function setField(field) {
    return (value) => setForm((prev) => ({ ...prev, [field]: value }))
  }

  async function handleSave() {
    setSaving(true)
    try {
      const requests = [infrastructure.update(buildMiscPayload(form))]
      if (form.analyticsMode && form.analyticsMode !== initialAnalyticsMode) {
        requests.push(infrastructure.updateAnalyticsMode(form.analyticsMode))
      }
      await Promise.all(requests)
      setInitialAnalyticsMode(form.analyticsMode)
      notifications.show({
        color: 'green',
        title: 'Saved',
        message: 'Infrastructure configuration saved successfully!',
      })
    } catch {
      notifications.show({
        color: 'red',
        title: 'Error',
        message: 'Failed to save infrastructure configuration',
      })
    } finally {
      setSaving(false)
    }
  }

  if (showLoader) return <PageLoader h={400} />

  return (
    <>
      <Group justify="space-between" align="flex-start" mb="xxxlAlt">
        <Title order={1}>Infrastructure</Title>
        <Button size="md" loading={saving} onClick={handleSave}>
          Save
        </Button>
      </Group>

      <Stack gap="xxlAlt">
        <SectionRow
          title="Product analytics"
          description="Help us improve Hoop by sharing usage data. Access and resources information are not collected."
        >
          <Stack gap="sm">
            {ANALYTICS_OPTIONS.map((option) => (
              <SelectionCard
                key={option.value}
                icon={option.icon}
                title={option.title}
                description={option.description}
                selected={form.analyticsMode === option.value}
                onClick={() => setField('analyticsMode')(option.value)}
              />
            ))}
          </Stack>
        </SectionRow>

        <SectionRow
          title="gRPC configuration"
          description="Specify the gRPC endpoint URL for establishing secure connections between Hoop agents and your gateway infrastructure."
          callout={
            <DocsBtnCallOut
              text="Learn more about gRPC"
              href={docsUrl.clients.commandLine.managingConfiguration}
            />
          }
        >
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              gRPC URL
            </Text>
            <TextInput
              placeholder="e.g. grpcs://yourgateway-domain.tld:443"
              value={form.grpcUrl}
              onChange={(e) => setField('grpcUrl')(e.currentTarget.value)}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="PostgreSQL Proxy Port"
          description="Organization-wide default for local PostgreSQL proxy port forwarding."
        >
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              Proxy Port
            </Text>
            <TextInput
              placeholder="e.g. 5432"
              value={form.postgresPort}
              onChange={(e) => setField('postgresPort')(e.currentTarget.value)}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="SSH Proxy Port"
          description="Organization-wide default for local SSH proxy port forwarding."
        >
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              Proxy Port
            </Text>
            <TextInput
              placeholder="e.g. 22"
              value={form.sshPort}
              onChange={(e) => setField('sshPort')(e.currentTarget.value)}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="RDP Proxy Port"
          description="Organization-wide default for local Remote Desktop Protocol proxy port forwarding."
        >
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              Proxy Port
            </Text>
            <TextInput
              placeholder="e.g. 13389"
              value={form.rdpPort}
              onChange={(e) => setField('rdpPort')(e.currentTarget.value)}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="HTTP Proxy Port"
          description="Organization-wide default for local HTTP proxy port forwarding."
        >
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              HTTP Proxy Port
            </Text>
            <TextInput
              placeholder="e.g. 18888"
              value={form.httpPort}
              onChange={(e) => setField('httpPort')(e.currentTarget.value)}
            />
          </Stack>
        </SectionRow>
      </Stack>
    </>
  )
}

export default SettingsInfrastructure
