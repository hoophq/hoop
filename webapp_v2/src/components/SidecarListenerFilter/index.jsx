import { useEffect, useMemo, useState } from 'react'
import { Anchor, Box, Group, Loader, Popover, Stack, Text, UnstyledButton } from '@mantine/core'
import { Check, Container, Search, X } from 'lucide-react'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import { usesConfigFile } from '@/pages/Sidecars/config'
import { useSidecarStore } from '@/stores/useSidecarStore'
import classes from './SidecarListenerFilter.module.css'

const LABEL = 'Sidecar / Listener'

// One node per sidecar, its named listeners under it. A nameless listener is
// left out: a rule binds to a listener by name, so none can target it.
function buildTree(sidecars) {
  return [...sidecars]
    .sort((a, b) => (a.name ?? '').localeCompare(b.name ?? ''))
    .map((sc) => ({
      id: sc.id,
      name: sc.name ?? '',
      configFile: usesConfigFile(sc),
      listeners: (sc.configuration?.listeners ?? [])
        .filter((l) => l?.name)
        .map((l) => ({ name: l.name, protocol: l.protocol ?? '' })),
    }))
}

// A sidecar whose name matches keeps all its listeners. Otherwise only the
// listeners that match stay, under their sidecar.
function filterTree(tree, search) {
  const q = search.trim().toLowerCase()
  if (q === '') return tree
  return tree
    .map((node) => {
      if (node.name.toLowerCase().includes(q)) return node
      const listeners = node.listeners.filter(
        (l) => l.name.toLowerCase().includes(q) || l.protocol.toLowerCase().includes(q),
      )
      return listeners.length > 0 ? { ...node, listeners } : null
    })
    .filter(Boolean)
}

// The check keeps its width on every row, so the right-hand hints line up.
function Row({ label, hint, strong, indent, selected, onClick }) {
  return (
    <UnstyledButton
      className={classes.row}
      onClick={onClick}
      py="xs"
      pr="sm"
      pl={indent ? 'xl' : 'sm'}
      w="100%"
    >
      <Group gap="sm" wrap="nowrap">
        <Text size="sm" fw={strong ? 600 : undefined} truncate="end" title={label} miw={0} flex={1}>
          {label}
        </Text>
        {hint && (
          <Text size="xs" c="dimmed" flex="0 0 auto">
            {hint}
          </Text>
        )}
        <Box w={14} flex="0 0 auto">
          {selected && <Check size={14} />}
        </Box>
      </Group>
    </UnstyledButton>
  )
}

/**
 * Filters a rule list by where the control plane distributes each rule: a
 * whole sidecar, or one of its listeners. The control plane's counterpart of
 * the gateway's Resource Role and Attribute filters, which have nothing to
 * match there.
 *
 * `selected` is `{ sidecarId, listenerName }`, with an empty listener name for
 * the whole sidecar, or null. The fleet comes from useSidecarStore, which this
 * loads on mount and offers only once that read answers; the caller matches
 * its rules against the same fleet with `boundRuleNames`
 * (pages/Sidecars/config.js). A fleet that fails later clears the selection
 * through `onClear`.
 */
export default function SidecarListenerFilter({ selected, onSelect, onClear }) {
  const sidecars = useSidecarStore((s) => s.sidecars)
  const loading = useSidecarStore((s) => s.loading)
  const error = useSidecarStore((s) => s.error)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')

  useEffect(() => {
    fetchSidecars()
  }, [fetchSidecars])

  const tree = useMemo(() => buildTree(sidecars), [sidecars])
  const visible = useMemo(() => filterTree(tree, search), [tree, search])

  const selectedNode = selected ? tree.find((n) => n.id === selected.sidecarId) : null
  const selectedLabel = selected
    ? [selectedNode?.name ?? 'Unknown sidecar', selected.listenerName].filter(Boolean).join(': ')
    : null

  const close = () => {
    setOpen(false)
    setSearch('')
  }

  const pick = (sidecarId, listenerName) => {
    onSelect({ sidecarId, listenerName })
    close()
  }

  const isSelected = (sidecarId, listenerName) =>
    selected?.sidecarId === sidecarId && (selected?.listenerName ?? '') === listenerName

  // The store keeps the last fleet through a refresh and after a failed one.
  // Offering it would match rules against bindings that may have changed since,
  // so a pending read shows the loader and a failed one the error, whatever the
  // store still holds. A fleet that did not load is not an empty fleet either.
  //
  // The fleet can also load without its bindings (bound_rules_unavailable).
  // Every rule would then read as bound nowhere, so that is a failure too.
  const bindingsUnavailable = sidecars.some((sc) => sc.bound_rules_unavailable)
  const failed = !loading && (!!error || bindingsUnavailable)

  // A selection made before a refresh that then fails is dropped, so the list
  // does not read "no rules match" off bindings the fleet no longer has.
  useEffect(() => {
    if (selected && failed) onClear()
  }, [selected, failed, onClear])

  let body
  if (loading) {
    body = (
      <Group justify="center" py="md">
        <Loader size="xs" />
      </Group>
    )
  } else if (failed) {
    body = (
      <Stack gap={4} px="sm" py="md">
        <Text size="xs" c="dimmed">
          {error ? 'Sidecars could not be loaded.' : 'The rules bound to each sidecar could not be loaded.'}
        </Text>
        <Anchor component="button" type="button" size="xs" onClick={() => fetchSidecars()}>
          Try again
        </Anchor>
      </Stack>
    )
  } else if (visible.length === 0) {
    body = (
      <Box px="sm" py="md">
        <Text size="xs" c="dimmed" fs="italic">
          {search ? 'No sidecar or listener found' : 'No sidecars yet'}
        </Text>
      </Box>
    )
  } else {
    body = (
      <Stack gap={0} mah={288} className={classes.options}>
        {visible.map((node) => (
          <Box key={node.id}>
            <Row
              strong
              label={node.name}
              // A sidecar on its config file has no control plane rules: the
              // switch to the file unbinds them.
              hint={node.configFile ? 'Uses config file' : 'All listeners'}
              selected={isSelected(node.id, '')}
              onClick={() => pick(node.id, '')}
            />
            {node.listeners.map((l) => (
              <Row
                key={l.name}
                indent
                label={l.name}
                hint={l.protocol.toLowerCase()}
                selected={isSelected(node.id, l.name)}
                onClick={() => pick(node.id, l.name)}
              />
            ))}
          </Box>
        ))}
      </Stack>
    )
  }

  return (
    <Popover opened={open} onChange={setOpen} position="bottom-start" width={320} withinPortal>
      {/* The trigger of ValueFilter and AsyncValueFilter, so the filter bar
          reads the same in both products. */}
      <Popover.Target>
        <Button
          variant={selected ? 'light' : 'default'}
          color="gray"
          fz="sm"
          c="dimmed"
          onClick={() => setOpen((value) => !value)}
          leftSection={<Container size={16} />}
          rightSection={
            selected ? (
              <X
                size={14}
                onClick={(event) => {
                  event.stopPropagation()
                  onClear()
                  close()
                }}
              />
            ) : null
          }
        >
          {selectedLabel ?? LABEL}
        </Button>
      </Popover.Target>
      <Popover.Dropdown p="xs">
        <Stack gap="xs">
          {selected && (
            <UnstyledButton
              className={classes.row}
              px="sm"
              py="xs"
              onClick={() => {
                onClear()
                close()
              }}
            >
              <Text size="sm" c="dimmed">
                Clear filter
              </Text>
            </UnstyledButton>
          )}
          <TextInput
            placeholder="Search a sidecar or a listener"
            value={search}
            onChange={(event) => setSearch(event.currentTarget.value)}
            leftSection={<Search size={14} />}
          />
          {body}
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}
