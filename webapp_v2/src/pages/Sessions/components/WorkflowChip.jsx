import { useNavigate } from 'react-router-dom'
import { Workflow } from 'lucide-react'
import Button from '@/components/Button'
import Tooltip from '@/components/Tooltip'

// Second consumer: Wave 6 session details header (B6.1).

/** Port of `workflow-chip` (session_item.cljs:34-51). */
export default function WorkflowChip({ correlationId }) {
  const navigate = useNavigate()
  if (!correlationId || !String(correlationId).trim()) return null

  const label =
    correlationId.length > 18 ? `${correlationId.slice(0, 16)}…` : correlationId

  const open = (event) => {
    // The whole row navigates to the session; this chip goes somewhere else.
    // v1 stopped propagation on click only — without the keydown guard, Enter on
    // a focused chip would fire the chip AND the row's link.
    event.stopPropagation()
    navigate(`/workflows/${encodeURIComponent(correlationId)}`)
  }

  return (
    <Tooltip label={`View workflow ${correlationId}`}>
      <Button
        size="xs"
        variant="light"
        color="gray"
        leftSection={<Workflow size={12} />}
        onClick={open}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') event.stopPropagation()
        }}
      >
        {label}
      </Button>
    </Tooltip>
  )
}
