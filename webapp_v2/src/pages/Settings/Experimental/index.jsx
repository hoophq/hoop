import { useState, useEffect } from 'react'
import { Group, Stack, Switch, Text, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useMinDelay } from '@/hooks/useMinDelay'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import Table from '@/components/Table'
import Badge from '@/components/Badge'
import { featureFlagsService } from '@/services/featureFlags'

const STABILITY_COLOR = {
  experimental: 'orange',
  beta: 'indigo',
}

export default function SettingsExperimental() {
  const [flags, setFlags] = useState([])
  const [loading, setLoading] = useState(true)
  const [pending, setPending] = useState(new Set())

  const showLoader = useMinDelay(loading)

  useEffect(() => {
    featureFlagsService
      .list()
      .then((res) => setFlags(res.data ?? []))
      .catch(() => {
        notifications.show({
          color: 'red',
          title: 'Error',
          message: 'Failed to load feature flags',
        })
      })
      .finally(() => setLoading(false))
  }, [])

  async function handleToggle(flagName, newValue) {
    const previous = flags.find((f) => f.name === flagName)?.enabled

    setPending((prev) => new Set(prev).add(flagName))
    setFlags((prev) =>
      prev.map((f) => (f.name === flagName ? { ...f, enabled: newValue } : f))
    )

    try {
      const res = await featureFlagsService.update(flagName, { enabled: newValue })
      setFlags((prev) =>
        prev.map((f) => (f.name === flagName ? res.data : f))
      )
    } catch {
      setFlags((prev) =>
        prev.map((f) => (f.name === flagName ? { ...f, enabled: previous } : f))
      )
      notifications.show({
        color: 'red',
        title: 'Error',
        message: `Failed to update flag "${flagName}"`,
      })
    } finally {
      setPending((prev) => {
        const next = new Set(prev)
        next.delete(flagName)
        return next
      })
    }
  }

  if (showLoader) return <PageLoader h={400} />

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>Experimental features</Title>
        <Text c="dimmed" size="lg">
          Enable or disable experimental and beta features for your organization.
        </Text>
      </Stack>

      {flags.length === 0 ? (
        <EmptyState
          title="No feature flags available"
          description="There are no experimental features configured for this environment."
        />
      ) : (
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th w={220}>Flag</Table.Th>
              <Table.Th>Description</Table.Th>
              <Table.Th w={130}>Stability</Table.Th>
              <Table.Th w={180}>Components</Table.Th>
              <Table.Th w={110}>Enabled</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {flags.map((flag) => (
              <Table.Tr key={flag.name}>
                <Table.Td>
                  <Text size="sm" fw={500}>{flag.name}</Text>
                </Table.Td>
                <Table.Td>
                  <Text size="sm" c="dimmed">{flag.description}</Text>
                </Table.Td>
                <Table.Td>
                  <Badge
                    variant="light"
                    color={STABILITY_COLOR[flag.stability] ?? 'gray'}
                  >
                    {flag.stability}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  <Group gap="xs" wrap="wrap">
                    {(flag.components ?? []).map((c) => (
                      <Badge key={c} variant="outline" color="gray" size="xs">
                        {c}
                      </Badge>
                    ))}
                  </Group>
                </Table.Td>
                <Table.Td>
                  <Group gap="xs">
                    <Switch
                      checked={flag.enabled ?? false}
                      disabled={pending.has(flag.name)}
                      onChange={(e) => handleToggle(flag.name, e.currentTarget.checked)}
                    />
                    <Text size="sm">{flag.enabled ? 'On' : 'Off'}</Text>
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  )
}
