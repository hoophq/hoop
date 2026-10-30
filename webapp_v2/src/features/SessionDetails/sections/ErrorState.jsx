import EmptyState from '@/layout/EmptyState'

// The session could not be read: it does not exist, or the user may not see it.
export default function ErrorState({ error }) {
  return <EmptyState compact title="This session could not be loaded" description={error?.message} />
}
