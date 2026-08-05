import { PanelRightOpen } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import { useNativeAccessStore } from '@/stores/useNativeAccessStore'
import { useNativeConnectionsStore } from '@/stores/useNativeConnectionsStore'
import { DRAWER_ID } from '@/features/NativeConnections/constants'
import classes from './Header.module.css'

/**
 * Opens the Native Connections drawer, and carries the active-session count.
 *
 * That badge matters: it is the replacement for the floating draggable card's
 * always-visible "you have a live session" cue, which a closed drawer loses.
 *
 * aria-controls is emitted even while the drawer is unmounted. Making the id
 * always resolvable would mean keepMounted, i.e. mounting a hidden drawer on
 * every route including every CLJS one — a worse trade than an axe
 * aria-valid-attr-value note. Do not "fix" this by adding keepMounted.
 */
export function NativeConnectionsButton() {
  const opened = useNativeConnectionsStore((s) => s.opened)
  const toggle = useNativeConnectionsStore((s) => s.toggle)
  const activeCount = useNativeAccessStore((s) => Object.keys(s.activeByName).length)

  const label = activeCount > 0 ? `Native Connections, ${activeCount} active` : 'Native Connections'

  const shared = {
    onClick: toggle,
    'aria-haspopup': 'dialog',
    'aria-expanded': opened,
    'aria-controls': DRAWER_ID,
    'aria-label': label,
  }

  return (
    <>
      <Button
        {...shared}
        visibleFrom="sm"
        variant="default"
        size="sm"
        className={classes.nativeButton}
        leftSection={<PanelRightOpen size={16} aria-hidden="true" />}
        rightSection={
          activeCount > 0 ? (
            <Badge variant="active" radius="xl" size="xs">
              {activeCount}
            </Badge>
          ) : null
        }
      >
        Native Connections
      </Button>

      {/* The label plus a Burger would not fit next to a 360px viewport. */}
      <ActionIcon {...shared} hiddenFrom="sm" variant="default" size="lg" className={classes.nativeButton}>
        <PanelRightOpen size={16} aria-hidden="true" />
      </ActionIcon>
    </>
  )
}
