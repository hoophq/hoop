import { useEffect, useMemo, useState } from 'react'
import { Group, Image, Stack, Text } from '@mantine/core'
import { Search } from 'lucide-react'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import Table from '@/components/Table'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import { useMinDelay } from '@/hooks/useMinDelay'
import { protocolInfo } from '@/pages/Sidecars/config'
import { sidecarsService } from '@/services/sidecars'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { showSnackbar } from '@/utils/snackbar'

const SPLIT_CHARS = [',', ' ']

function errorMessage(error) {
  return error?.response?.data?.message ?? error?.message
}

function toMap(data) {
  return Object.fromEntries((data?.listeners ?? []).map((l) => [l.name, l.channels ?? []]))
}

function sameChannels(a = [], b = []) {
  return a.length === b.length && a.every((c, i) => c === b[i])
}

/**
 * The control plane's counterpart of the gateway's Connections tab: every
 * listener of every sidecar in one list, each with its channels inline. A
 * listener is the unit, as for guardrails, data masking and the analyzer. A
 * listener with no channel posts to the fallback channel of Configurations.
 *
 * The API stores a sidecar's listeners as one set, so a row saves its own
 * change on top of what is stored for its sidecar, never other rows' drafts.
 * One save runs at a time: a second one built before the first answered would
 * send the old set and undo it.
 */
function SidecarSlackChannelsTab() {
  const sidecars = useSidecarStore((s) => s.sidecars)
  const loading = useSidecarStore((s) => s.loading)
  const error = useSidecarStore((s) => s.error)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)
  const getIcon = useConnectionIconGetter()

  // sidecar id -> { listener: channels }, as stored.
  const [saved, setSaved] = useState({})
  // "sidecarId/listener" -> channels, while edited.
  const [drafts, setDrafts] = useState({})
  const [channelsStatus, setChannelsStatus] = useState('idle')
  const [savingKey, setSavingKey] = useState(null)
  const [search, setSearch] = useState('')

  useEffect(() => {
    fetchSidecars()
  }, [fetchSidecars])

  useEffect(() => {
    if (loading || sidecars.length === 0) return
    let current = true
    setChannelsStatus('loading')
    Promise.all(sidecars.map((sc) => sidecarsService.slackChannels(sc.id).then(({ data }) => [sc.id, toMap(data)])))
      .then((entries) => {
        if (!current) return
        setSaved(Object.fromEntries(entries))
        setChannelsStatus('ready')
      })
      .catch(() => {
        if (current) setChannelsStatus('error')
      })
    return () => {
      current = false
    }
  }, [sidecars, loading])

  const rows = useMemo(() => {
    const term = search.trim().toLowerCase()
    return [...sidecars]
      .sort((a, b) => (a.name ?? '').localeCompare(b.name ?? ''))
      .flatMap((sc) =>
        (sc.configuration?.listeners ?? [])
          .filter((l) => l.name)
          .map((l) => ({ sidecar: sc, listener: l, key: `${sc.id}/${l.name}` })),
      )
      .filter(
        ({ sidecar, listener }) =>
          !term ||
          sidecar.name?.toLowerCase().includes(term) ||
          listener.name.toLowerCase().includes(term) ||
          (listener.protocol ?? '').toLowerCase().includes(term),
      )
  }, [sidecars, search])

  async function handleSave(sidecar, listener, key) {
    if (savingKey !== null) return
    // Only this listener: the server leaves the others as they are, so a
    // save never undoes another admin's edit of a different row.
    const submitted = drafts[key] ?? []
    setSavingKey(key)
    try {
      const { data } = await sidecarsService.updateSlackChannels(sidecar.id, {
        listeners: [{ name: listener, channels: submitted }],
      })
      setSaved((prev) => ({ ...prev, [sidecar.id]: toMap(data) }))
      // An edit typed while the save ran is newer than what was saved: keep it.
      setDrafts((prev) => {
        if (!sameChannels(prev[key] ?? [], submitted)) return prev
        const next = { ...prev }
        delete next[key]
        return next
      })
      showSnackbar({ level: 'success', text: `Slack channels saved for ${listener}.` })
    } catch (err) {
      showSnackbar({ level: 'error', text: 'Failed to save Slack channels.', description: errorMessage(err) })
    } finally {
      setSavingKey(null)
    }
  }

  const showLoader = useMinDelay(loading || channelsStatus === 'loading')
  if (showLoader) return <PageLoader h={200} />
  if (error) return <PageLoader error h={200} message="Failed to load sidecars." />
  if (channelsStatus === 'error') return <PageLoader error h={200} message="Failed to load Slack channels." />

  return (
    <Stack gap="md">
      <Group justify="space-between" align="center" wrap="wrap" gap="md">
        <Text size="sm" c="dimmed">
          Reviews go to each listener's channels. A listener without one uses the fallback channel.
        </Text>
        <TextInput
          placeholder="Search listeners"
          leftSection={<Search size={16} />}
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
          w={280}
        />
      </Group>

      {rows.length === 0 ? (
        <Text size="sm" c="dimmed">
          {search.trim() ? 'No listeners match your search.' : 'No listeners yet. Add one on the Sidecars page.'}
        </Text>
      ) : (
        <Table scrollable>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Listener</Table.Th>
              <Table.Th>Sidecar</Table.Th>
              <Table.Th>Channel IDs</Table.Th>
              <Table.Th aria-label="Actions" w={96} />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map(({ sidecar, listener, key }) => {
              const stored = saved[sidecar.id]?.[listener.name] ?? []
              const value = drafts[key] ?? stored
              const info = protocolInfo(listener.protocol)
              return (
                <Table.Tr key={key}>
                  <Table.Td miw={160}>
                    <Group gap={8} wrap="nowrap">
                      {info.subtype && <Image src={getIcon({ subtype: info.subtype })} alt="" w={16} h={16} fit="contain" />}
                      <Text size="sm" fw={600}>
                        {listener.name}
                      </Text>
                    </Group>
                  </Table.Td>
                  <Table.Td miw={120}>
                    <Text size="sm" c="dimmed">
                      {sidecar.name}
                    </Text>
                  </Table.Td>
                  <Table.Td miw={320}>
                    <TagsInput
                      aria-label={`Slack channels for ${listener.name}`}
                      placeholder={value.length === 0 ? 'Fallback channel' : undefined}
                      splitChars={SPLIT_CHARS}
                      value={value}
                      onChange={(next) => setDrafts((prev) => ({ ...prev, [key]: next }))}
                    />
                  </Table.Td>
                  <Table.Td>
                    <Button
                      variant="default"
                      size="xs"
                      disabled={sameChannels(value, stored) || (savingKey !== null && savingKey !== key)}
                      loading={savingKey === key}
                      onClick={() => handleSave(sidecar, listener.name, key)}
                    >
                      Save
                    </Button>
                  </Table.Td>
                </Table.Tr>
              )
            })}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  )
}

export default SidecarSlackChannelsTab
