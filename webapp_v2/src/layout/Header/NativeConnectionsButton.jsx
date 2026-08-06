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
 * aria-controls is emitted even while the drawer is unmounted. Making the id
 * always resolvable would mean keepMounted, i.e. mounting a hidden drawer on
 * every route including every CLJS one — a worse trade than an axe
 * aria-valid-attr-value note. Do not "fix" this by adding keepMounted.
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
