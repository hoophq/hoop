import Button from '@/components/Button'
import { useNativeAccessStore, FLOW_STATUS } from '@/stores/useNativeAccessStore'
import { ROW_STATE } from './rowState'
import classes from './NativeConnections.module.css'

// Only the two connectable-but-not-connected states carry an action. A row with
// a live session shows its status and a chevron instead; offline, revoked and
// pending-review rows have nothing to offer here.
const LABELS = {
  [ROW_STATE.IDLE]: 'Connect',
  [ROW_STATE.NEEDS_REVIEW]: 'Ask access',
}

/**
 * The row's primary action, rendered as a sibling of Accordion.Control rather
 * than inside it — the control is a <button>, and nesting one button in another
 * is invalid HTML that breaks both keyboard navigation and the accessible name.
 *
 * Connecting hangs off this button and not off expanding the row: a review-gated
 * role must not fire a request just because someone opened it.
 */
export function RoleRowAction({ role, state }) {
  const beginConnect = useNativeAccessStore((s) => s.beginConnect)
  const status = useNativeAccessStore((s) => s.statusByName[role.name])

  const label = LABELS[state]
  if (!label) return null

  const busy = status === FLOW_STATUS.CHECKING || status === FLOW_STATUS.REQUESTING

  return (
    <div className={classes.rowAction}>
      <Button
        variant="light"
        color="indigo"
        size="sm"
        h={32}
        loading={busy}
        onClick={() => beginConnect(role.name)}
      >
        {label}
      </Button>
    </div>
  )
}
