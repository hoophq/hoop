import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { deniedLabel } from '../../decoders/liveTail'

// Marks a stream row whose statement a guardrail denied.
export default function DeniedBadge({ rules }) {
  return (
    <Tooltip label={deniedLabel(rules)}>
      <Badge color="red" variant="filled" tag flex="none">
        Denied
      </Badge>
    </Tooltip>
  )
}
