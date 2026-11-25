import { useEventRoutingStore } from '../store'
import SubscriptionsList from './SubscriptionsList'
import EmptyState from '@/layout/EmptyState'

export default function SubscriptionsTab({ onOpenCreate }) {
  const { subscriptions } = useEventRoutingStore()

  if (subscriptions.data.length === 0) {
    return (
      <EmptyState
        compact
        title="No subscriptions yet"
        description="Subscribe a runbook to platform events to react to them automatically."
        action={{ label: 'Create subscription', onClick: onOpenCreate }}
      />
    )
  }

  return <SubscriptionsList />
}
