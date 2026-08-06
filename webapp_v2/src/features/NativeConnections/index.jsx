import { useEffect, useMemo } from 'react'
import { Drawer, ScrollArea, Stack } from '@mantine/core'
import { useNativeConnectionsStore } from '@/stores/useNativeConnectionsStore'
import { useNativeAccessStore } from '@/stores/useNativeAccessStore'
import { DrawerHeader } from './DrawerHeader'
import { SearchBar } from './SearchBar'
import { RoleList } from './RoleList'
import { RequestAccessModal } from './RequestAccessModal'
import { useCljsBridge } from './useCljsBridge'
import { matchesQuery } from './helpers'
import {
  DRAWER_ID,
  DRAWER_OFFSET,
  DRAWER_TITLE_ID,
  DRAWER_WIDTH,
  DRAWER_Z_INDEX,
  MAX_RENDERED_ROWS,
} from './constants'

// Structural shell slots: Mantine's own Drawer CSS outranks classNames here, so
// these go through `styles` with constants — the exception CLAUDE.md sanctions.
const DRAWER_CONTENT_STYLES = { display: 'flex', flexDirection: 'column' }
const DRAWER_BODY_STYLES = {
  padding: 0,
  height: '100%',
  display: 'flex',
  flexDirection: 'column',
  minHeight: 0,
}

/**
 * The Native Connections drawer — the single surface for native access.
 *
 * Mounted once from layout/Layout.jsx, which wraps both React routes and the
 * ClojureApp catch-all, so it is reachable from every page.
 *
 * No src/components/Drawer wrapper: the app's only other Drawer (the mobile
 * sidebar in Layout) wants the opposite geometry on every axis, so a shared
 * wrapper would be a pass-through with two mutually exclusive prop bundles.
 * Revisit when a third drawer shows up.
 */
export default function NativeConnectionsDrawer() {
  const opened = useNativeConnectionsStore((s) => s.opened)
  const close = useNativeConnectionsStore((s) => s.close)
  const query = useNativeConnectionsStore((s) => s.query)
  const setQuery = useNativeConnectionsStore((s) => s.setQuery)
  const clearQuery = useNativeConnectionsStore((s) => s.clearQuery)
  const expanded = useNativeConnectionsStore((s) => s.expanded)
  const setExpanded = useNativeConnectionsStore((s) => s.setExpanded)
  const roles = useNativeConnectionsStore((s) => s.roles)
  const loading = useNativeConnectionsStore((s) => s.loading)
  const error = useNativeConnectionsStore((s) => s.error)
  const load = useNativeConnectionsStore((s) => s.load)
  const refresh = useNativeConnectionsStore((s) => s.refresh)

  const activeByName = useNativeAccessStore((s) => s.activeByName)
  const loadActive = useNativeAccessStore((s) => s.loadActive)

  useCljsBridge()

  // Active credentials are loaded once at mount so the header badge is correct
  // before the drawer is ever opened, then refreshed whenever it opens.
  useEffect(() => {
    loadActive()
  }, [loadActive])

  useEffect(() => {
    if (!opened) return
    load()
    loadActive()
  }, [opened, load, loadActive])

  const { visible, total, matched, shown, truncated } = useMemo(() => {
    // A role whose native access was switched off is kept only while it still
    // has a live credential, so the user can still disconnect it.
    const listable = roles.filter(
      (role) => role.accessModeConnect === 'enabled' || activeByName[role.name]
    )
    const filtered = listable.filter((role) => matchesQuery(role, query))
    const capped = filtered.slice(0, MAX_RENDERED_ROWS)
    return {
      visible: capped,
      // `total` is every listable role; `matched` is how many the query hit.
      // The count line needs both — with a search applied it has to report
      // against the matches, not against the whole list.
      total: listable.length,
      matched: filtered.length,
      shown: capped.length,
      truncated: filtered.length > capped.length,
    }
  }, [roles, activeByName, query])

  return (
    <>
    <Drawer
      id={DRAWER_ID}
      opened={opened}
      onClose={close}
      position="right"
      size={DRAWER_WIDTH}
      offset={DRAWER_OFFSET}
      radius="lg"
      padding={0}
      withCloseButton={false}
      overlayProps={{ backgroundOpacity: 0.5 }}
      zIndex={DRAWER_Z_INDEX}
      aria-labelledby={DRAWER_TITLE_ID}
      transitionProps={{ duration: 250, timingFunction: 'ease' }}
      styles={{ content: DRAWER_CONTENT_STYLES, body: DRAWER_BODY_STYLES }}
    >
      <Stack gap={0} h="100%">
        <DrawerHeader onClose={close} />
        <SearchBar
          query={query}
          onQueryChange={setQuery}
          onClearQuery={clearQuery}
          total={total}
          matched={matched}
          shown={shown}
          truncated={truncated}
        />
        <ScrollArea flex={1} type="auto">
          <RoleList
            roles={visible}
            activeByName={activeByName}
            loading={loading && !roles.length}
            error={error}
            query={query}
            expanded={expanded}
            onExpandedChange={setExpanded}
            onRetry={refresh}
          />
        </ScrollArea>
      </Stack>
    </Drawer>

    {/* Outside the Drawer so it is not clipped by it, and mounted once rather
        than per row — the drawer stays visible behind it by design. */}
    <RequestAccessModal />
    </>
  )
}
