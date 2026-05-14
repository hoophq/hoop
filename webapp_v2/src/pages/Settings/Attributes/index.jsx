import { useState, useEffect, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Button, Divider, Group, Image, Paper, Popover, Stack, Text, Title } from '@mantine/core'
import { Check, Rotate3d, Search, X } from 'lucide-react'
import { useMinDelay } from '@/hooks/useMinDelay'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import TextInput from '@/components/TextInput'
import { docsUrl } from '@/utils/docsUrl'
import { attributesService } from '@/services/attributes'
import { connectionsService } from '@/services/connections'
import { getConnectionIcon } from '@/utils/connectionIcons'

function ConnectionFilter({ value, onChange, connections }) {
  const [search, setSearch] = useState('')
  const [opened, setOpened] = useState(false)

  const filtered = search
    ? connections.filter((c) => c.name.toLowerCase().includes(search.toLowerCase()))
    : connections

  return (
    <Popover
      opened={opened}
      onChange={setOpened}
      width={360}
      position="bottom-start"
      withinPortal
    >
      <Popover.Target>
        <Button
          variant={value ? 'light' : 'outline'}
          color="gray"
          leftSection={<Rotate3d size={14} />}
          rightSection={
            value ? (
              <X
                size={14}
                onClick={(e) => {
                  e.stopPropagation()
                  onChange(null)
                }}
              />
            ) : null
          }
          onClick={() => { setSearch(''); setOpened((o) => !o) }}
        >
          {value ?? 'Resource Role'}
        </Button>
      </Popover.Target>
      <Popover.Dropdown p="sm">
        <Stack gap="sm">
          {value && (
            <Button
              variant="subtle"
              color="gray"
              size="sm"
              fullWidth
              onClick={() => { onChange(null); setOpened(false) }}
            >
              Clear filter
            </Button>
          )}
          <TextInput
            placeholder="Search resource roles"
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
            leftSection={<Search size={14} />}
          />
          <Box mah={280} style={{ overflowY: 'auto' }}>
            {filtered.length === 0 ? (
              <Text size="sm" c="dimmed" p="xs">No connections found</Text>
            ) : (
              <Stack gap={2}>
                {filtered.map((conn) => (
                  <Button
                    key={conn.name}
                    variant={conn.name === value ? 'light' : 'subtle'}
                    color="gray"
                    size="sm"
                    fullWidth
                    justify="flex-start"
                    onClick={() => { onChange(conn.name === value ? null : conn.name); setOpened(false) }}
                  >
                    <Group gap="xs" wrap="nowrap" w="100%" justify="space-between">
                      <Group gap="xs" wrap="nowrap">
                        <Image src={getConnectionIcon(conn)} w={16} h={16} fit="contain" />
                        <Text size="sm">{conn.name}</Text>
                      </Group>
                      {conn.name === value && <Check size={14} />}
                    </Group>
                  </Button>
                ))}
              </Stack>
            )}
          </Box>
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}

export default function Attributes() {
  const navigate = useNavigate()
  const [attributes, setAttributes] = useState([])
  const [connections, setConnections] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [filterConn, setFilterConn] = useState(null)

  const showLoader = useMinDelay(loading)

  useEffect(() => {
    async function fetchData() {
      try {
        const [attrsRes, connsRes] = await Promise.all([
          attributesService.list(),
          connectionsService.getConnections(),
        ])
        setAttributes(attrsRes.data?.data ?? [])
        const connList = Array.isArray(connsRes) ? connsRes : (connsRes?.results ?? [])
        setConnections(connList)
      } catch {
        setError('Failed to load attributes.')
      } finally {
        setLoading(false)
      }
    }
    fetchData()
  }, [])

  const filterableConnections = useMemo(() => {
    const usedNames = new Set()
    attributes.forEach((attr) => (attr.connection_names ?? []).forEach((n) => usedNames.add(n)))
    return connections.filter((c) => usedNames.has(c.name))
  }, [attributes, connections])

  const filtered = filterConn
    ? attributes.filter((attr) => (attr.connection_names ?? []).includes(filterConn))
    : attributes

  if (showLoader) return <PageLoader />
  if (error) return <PageLoader error={error} />

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="center">
        <Stack gap="xs" flex={1} miw={0}>
          <Title order={1}>Attributes</Title>
          <Text c="dimmed" size="lg">
            Properties that control how features and security policies apply to connections. Assign attributes to connections to automatically enforce consistent behaviors.
          </Text>
        </Stack>
        {attributes.length > 0 && (
          <Button onClick={() => navigate('/settings/attributes/new')}>Create a new Attribute</Button>
        )}
      </Group>

      {attributes.length === 0 ? (
        <EmptyState
          title="No Attributes configured in your Organization yet"
          action={{ label: 'Create a new Attribute', onClick: () => navigate('/settings/attributes/new') }}
          docsUrl={docsUrl.features.attributes}
          docsLabel="Attributes documentation"
        />
      ) : (
        <>
          {filterableConnections.length > 0 && (
            <Group>
              <ConnectionFilter
                value={filterConn}
                onChange={setFilterConn}
                connections={filterableConnections}
              />
            </Group>
          )}

          {filtered.length === 0 ? (
            <Text c="dimmed" size="sm">
              {`No attributes match connection '${filterConn}'.`}
            </Text>
          ) : (
            <Paper withBorder radius="md">
              {filtered.map((attr, idx) => (
                <div key={attr.name}>
                  {idx > 0 && <Divider />}
                  <Group justify="space-between" align="flex-start" p="md">
                    <Stack gap={2}>
                      <Text fw={600}>{attr.name ?? 'Unnamed Attribute'}</Text>
                      <Text size="sm" c="dimmed">
                        {attr.description ?? ''}
                      </Text>
                    </Stack>
                    <Button
                      variant="outline"
                      color="gray"
                      size="sm"
                      onClick={() => navigate(`/settings/attributes/edit/${attr.name}`)}
                    >
                      Configure
                    </Button>
                  </Group>
                </div>
              ))}
            </Paper>
          )}
        </>
      )}
    </Stack>
  )
}
