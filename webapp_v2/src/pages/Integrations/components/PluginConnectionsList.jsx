import { Fragment, useMemo, useState } from 'react'
import { Divider, Group, Stack, Text } from '@mantine/core'
import { Cable, Search } from 'lucide-react'
import TextInput from '@/components/TextInput'
import Switch from '@/components/Switch'

/**
 * Toggle list of all workspace connections for a plugin: a connection is
 * enabled when its id is present in plugin.connections. `renderAction`
 * optionally renders a per-row action (e.g. Slack's Configure button).
 * Purely presentational — data comes from usePlugin.
 */
function PluginConnectionsList({ plugin, connections, mutating, onToggle, renderAction }) {
  const [search, setSearch] = useState('')

  const filtered = useMemo(() => {
    const term = search.trim().toLowerCase()
    if (!term) return connections
    return connections.filter((c) => c.name?.toLowerCase().includes(term))
  }, [connections, search])

  const enabledIds = useMemo(
    () => new Set((plugin?.connections ?? []).map((c) => c.id)),
    [plugin]
  )

  return (
    <Stack gap="md">
      <TextInput
        placeholder="Search connections"
        leftSection={<Search size={16} />}
        value={search}
        onChange={(e) => setSearch(e.currentTarget.value)}
      />
      {filtered.length === 0 ? (
        <Text size="sm" c="dimmed" fs="italic">
          {search.trim() ? 'No connections match your search.' : `You don't have any connections`}
        </Text>
      ) : (
        <Stack gap={0}>
          {filtered.map((connection, index) => {
            const enabled = enabledIds.has(connection.id)
            return (
              <Fragment key={connection.id}>
                <Group justify="space-between" py="sm">
                  <Group gap="sm">
                    <Cable size={16} />
                    <Text size="sm" fw={600}>
                      {connection.name}
                    </Text>
                    <Switch
                      checked={enabled}
                      disabled={mutating}
                      onChange={() => onToggle(connection, !enabled)}
                      aria-label={`Toggle ${connection.name}`}
                    />
                  </Group>
                  {renderAction ? renderAction(connection, enabled) : null}
                </Group>
                {index < filtered.length - 1 && <Divider />}
              </Fragment>
            )
          })}
        </Stack>
      )}
    </Stack>
  )
}

export default PluginConnectionsList
