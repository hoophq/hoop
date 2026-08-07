import { PanelRightOpen } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import { useNativeConnectionsStore } from '@/stores/useNativeConnectionsStore'
import { DRAWER_ID } from '@/features/NativeConnections/constants'
import classes from './Header.module.css'

/**
 * Opens the Native Connections drawer.
 *
 * No active-session count: the Figma header carries the label alone. The live
 * session cue lives on the rows themselves (countdown badge and open
 * indicator), which is where it can say which connection it refers to.
 *
 * aria-controls resolves to Drawer.Content, which is the node that carries
 * role="dialog" — the drawer uses Mantine's compound API for exactly that
 * reason. It is still absent while the drawer is closed (no keepMounted, so no
 * hidden drawer on every CLJS route), which is the accepted trade.
 */
export function NativeConnectionsButton() {
  const opened = useNativeConnectionsStore((s) => s.opened)
  const toggle = useNativeConnectionsStore((s) => s.toggle)

  const shared = {
    onClick: toggle,
    'aria-haspopup': 'dialog',
    'aria-expanded': opened,
    'aria-controls': DRAWER_ID,
    'aria-label': 'Native Connections',
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
