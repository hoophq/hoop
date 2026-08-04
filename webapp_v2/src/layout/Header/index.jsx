import { Box, Group } from '@mantine/core'
import { HeaderSearch } from './HeaderSearch'
import { NativeConnectionsButton } from './NativeConnectionsButton'
import { UserMenu } from './UserMenu'

/**
 * The global application header. Rendered by layout/Layout.jsx inside
 * AppShell.Header, so it sits above React routes AND the ClojureApp catch-all.
 *
 * `burger` is the mobile navigation toggle, owned by Layout because it drives
 * the mobile sidebar Drawer that also lives there.
 */
function AppHeader({ burger }) {
  return (
    <Group h="100%" px="md" gap="md" wrap="nowrap">
      {burger}

      {/* miw={0} or the flex child refuses to shrink below its content width
          and pushes the right-hand controls off screen on narrow viewports. */}
      <Box flex={1} miw={0}>
        <HeaderSearch />
      </Box>

      <Group gap="sm" wrap="nowrap">
        <NativeConnectionsButton />
        <UserMenu />
      </Group>
    </Group>
  )
}

export default AppHeader
