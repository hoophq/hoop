import EmptyState from '@/layout/EmptyState'

// An output with no events: it waits for an approval or an execution, or it
// still runs.
export default function EmptyStream() {
  return (
    <EmptyState
      compact
      title="This session needs approval or execution."
      description="Or maybe it's still running and you just need to wait it to end."
    />
  )
}
