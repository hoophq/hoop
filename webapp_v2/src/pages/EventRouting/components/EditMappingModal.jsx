import { useEffect, useMemo, useState } from 'react'
import { Box, Button, Card, Group, Select, Stack, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { ArrowRight } from 'lucide-react'
import Code from '@/components/Code'
import Modal from '@/components/Modal'
import { useEventRoutingStore } from '../store'

function sortRunbookParams(metadata) {
  return Object.entries(metadata || {})
    .map(([name, info]) => [name, info || {}])
    .sort(([aName, a], [bName, b]) => {
      const ao = typeof a.order === 'number' ? a.order : Number.POSITIVE_INFINITY
      const bo = typeof b.order === 'number' ? b.order : Number.POSITIVE_INFINITY
      if (ao !== bo) return ao - bo
      return aName.localeCompare(bName)
    })
}

// Strip the JSONPath wrapper for display in the Select. The Create page only
// stores top-level field references like "$.field_name"; preserve other shapes
// (custom paths) as-is and let the user re-pick if they want a clean field.
function fieldFromJsonPath(source) {
  if (typeof source !== 'string') return ''
  if (source.startsWith('$.') && !source.slice(2).includes('.') && !source.slice(2).includes('[')) {
    return source.slice(2)
  }
  return ''
}

export default function EditMappingModal({ sub, opened, onClose }) {
  const catalog = useEventRoutingStore((s) => s.catalog.data)
  const runbooksByConnection = useEventRoutingStore((s) => s.runbooksByConnection)
  const fetchRunbooksForConnection = useEventRoutingStore((s) => s.fetchRunbooksForConnection)
  const updateSubscription = useEventRoutingStore((s) => s.updateSubscription)
  const submitting = useEventRoutingStore((s) => s.submitting)

  // Make sure the runbook list for this connection is loaded so we can read
  // the runbook's metadata.
  useEffect(() => {
    if (opened && sub?.connectionName) {
      fetchRunbooksForConnection(sub.connectionName)
    }
  }, [opened, sub?.connectionName, fetchRunbooksForConnection])

  // Find the current runbook's metadata
  const runbookMetadata = useMemo(() => {
    if (!sub) return {}
    const repos = runbooksByConnection[sub.connectionName]?.data || []
    for (const repo of repos) {
      if (repo.repository !== sub.runbookRepository) continue
      const item = (repo.items || []).find((it) => it.name === sub.runbookFile)
      if (item) return item.metadata || {}
    }
    return {}
  }, [sub, runbooksByConnection])

  const runbookParams = useMemo(
    () => sortRunbookParams(runbookMetadata),
    [runbookMetadata],
  )

  const eventEntry = useMemo(
    () => catalog.find((e) => e.name === sub?.eventTypes?.[0]) || null,
    [catalog, sub],
  )

  const eventFieldOptions = useMemo(() => {
    const schema = eventEntry?.schema || []
    return schema.map((f) => ({
      value: f.name,
      label: f.type ? `${f.name}  ·  ${f.type}` : f.name,
    }))
  }, [eventEntry])

  // mapping state — keyed by runbook param name, value is event field name
  const [mapping, setMapping] = useState({})

  // Seed from the current parameterMapping on open. Convert `$.field` back to
  // bare `field` for the Select; leave unknown shapes blank so the user can
  // re-pick.
  useEffect(() => {
    if (!opened) return
    const seed = {}
    const entries = Object.entries(sub?.parameterMapping || {})
    for (const [paramName, source] of entries) {
      const field = fieldFromJsonPath(source)
      if (field) seed[paramName] = field
    }
    setMapping(seed)
  }, [opened, sub])

  const missingRequiredMapping = runbookParams.some(
    ([name, info]) => info.required && !mapping[name],
  )

  const setMappingFor = (paramName, fieldName) => {
    setMapping((prev) => {
      const next = { ...prev }
      if (fieldName) next[paramName] = fieldName
      else delete next[paramName]
      return next
    })
  }

  const handleSave = async () => {
    if (!sub || missingRequiredMapping) return
    const parameterMapping = Object.fromEntries(
      Object.entries(mapping).map(([param, field]) => [param, `$.${field}`]),
    )
    try {
      await updateSubscription(sub.id, { ...sub, parameterMapping })
      notifications.show({ message: 'Parameter mapping updated.', color: 'green' })
      onClose()
    } catch (e) {
      notifications.show({
        message: e?.response?.data?.message || 'Failed to update mapping.',
        color: 'red',
      })
    }
  }

  const runbooksStatus = runbooksByConnection[sub?.connectionName]?.status || 'idle'
  const isLoading = runbooksStatus === 'loading'

  return (
    <Modal opened={opened} onClose={onClose} title="Edit parameter mapping" size="xl">
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {'Map each runbook parameter to a field on the '}
          <Code>{sub?.eventTypes?.[0] || '—'}</Code>
          {' event payload.'}
        </Text>

        {isLoading ? (
          <Text size="xs" c="dimmed">Loading runbook parameters…</Text>
        ) : runbookParams.length === 0 ? (
          <Text size="xs" c="dimmed">
            This runbook declares no parameters. The dispatch runs without an input mapping.
          </Text>
        ) : eventFieldOptions.length === 0 ? (
          <Text size="xs" c="red">
            The subscribed event has no schema fields available to map.
          </Text>
        ) : (
          <Card padding={0} withBorder>
            <Stack gap={0}>
              <Group
                px="md"
                py="sm"
                wrap="nowrap"
                gap="md"
                bg="gray.0"
                style={{ borderBottom: '1px solid var(--mantine-color-default-border)' }}
              >
                <Text size="xs" fw={600} c="dimmed" tt="uppercase" style={{ flex: 1, minWidth: 0 }}>
                  Runbook parameter
                </Text>
                <Box w={16} />
                <Text size="xs" fw={600} c="dimmed" tt="uppercase" style={{ flex: 1.2, minWidth: 0 }}>
                  Event payload field
                </Text>
              </Group>

              {runbookParams.map(([paramName, paramInfo]) => {
                const required = !!paramInfo.required
                const value = mapping[paramName] || null
                return (
                  <Group
                    key={paramName}
                    p="md"
                    align="flex-start"
                    wrap="nowrap"
                    gap="md"
                    style={{ borderBottom: '1px solid var(--mantine-color-default-border)' }}
                  >
                    <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
                      <Group gap="xs" align="center">
                        <Code>{paramName}</Code>
                        {required && <Text size="xs" c="red">*</Text>}
                        {paramInfo.type && (
                          <Text size="xs" c="dimmed">{paramInfo.type}</Text>
                        )}
                      </Group>
                      {paramInfo.description && (
                        <Text size="xs" c="dimmed">{paramInfo.description}</Text>
                      )}
                    </Stack>
                    <ArrowRight
                      size={16}
                      color="var(--mantine-color-gray-6)"
                      style={{ marginTop: 6 }}
                    />
                    <Box style={{ flex: 1.2, minWidth: 0 }}>
                      <Select
                        placeholder={required ? 'Pick an event field' : 'Optional'}
                        data={eventFieldOptions}
                        value={value}
                        onChange={(v) => setMappingFor(paramName, v)}
                        searchable
                        clearable
                        error={required && !value ? 'Required' : undefined}
                      />
                    </Box>
                  </Group>
                )
              })}
            </Stack>
          </Card>
        )}

        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose}>Cancel</Button>
          <Button
            onClick={handleSave}
            disabled={missingRequiredMapping || isLoading}
            loading={submitting}
          >
            Save mapping
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
