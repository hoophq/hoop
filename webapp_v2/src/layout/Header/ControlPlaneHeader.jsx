import { Box, Group, Input } from '@mantine/core'
import { useOs } from '@mantine/hooks'
import { spotlight } from '@mantine/spotlight'
import { Search } from 'lucide-react'
import { UserMenu } from './UserMenu'
import classes from './Header.module.css'

// The control plane header, sibling of GatewayHeader.jsx. No Native Connections
// button: the control plane starts no data plane to connect to. The search
// button opens the Mantine Spotlight directly; GatewayHeaderSearch has to route
// to the CLJS palette on ClojureApp routes, which do not exist here.
function HeaderSearch() {
  const os = useOs()
  const isMac = os === 'macos' || os === 'ios'

  return (
    <Input
      component="button"
      type="button"
      pointer
      size="sm"
      aria-label="Search"
      aria-keyshortcuts={isMac ? 'Meta+K' : 'Control+K'}
      onClick={() => spotlight.open()}
      leftSection={<Search size={16} aria-hidden="true" />}
      classNames={{ input: classes.searchInput }}
    >
      <Input.Placeholder>{`Search (${isMac ? 'cmd' : 'ctrl'} + k)`}</Input.Placeholder>
    </Input>
  )
}

// `burger` is the mobile navigation toggle, owned by Layout because it drives
// the mobile sidebar Drawer that also lives there.
function AppHeader({ burger }) {
  return (
    <Group h="100%" px="md" gap="md" wrap="nowrap">
      {burger}

      {/* miw={0} or the flex child refuses to shrink below its content width
          and pushes the right-hand controls off screen on narrow viewports. */}
      <Box flex={1} miw={0}>
        <HeaderSearch />
      </Box>

      <UserMenu versionLabel="Control plane" />
    </Group>
  )
}

export default AppHeader
