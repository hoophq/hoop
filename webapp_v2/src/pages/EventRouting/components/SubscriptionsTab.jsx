import { useEventRoutingStore } from '../store'
import SubscriptionsList from './SubscriptionsList'
import EmptyState from './EmptyState'

export default function SubscriptionsTab({ onOpenCreate }) {
  const { subscriptions } = useEventRoutingStore()

  if (subscriptions.data.length === 0) {
    return <EmptyState onCreate={onOpenCreate} />
  }

  return <SubscriptionsList />
}
