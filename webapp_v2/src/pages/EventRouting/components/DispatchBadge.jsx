import { Badge } from '@mantine/core'
import { CheckCircle2, Clock, XCircle } from 'lucide-react'

export default function DispatchBadge({ status }) {
  if (status === 'delivered' || status === 'succeeded' || status === 'done') {
    return (
      <Badge color="green" variant="light" leftSection={<CheckCircle2 size={12} />}>
        Delivered
      </Badge>
    )
  }
  if (status === 'pending' || status === 'processing') {
    return (
      <Badge color="yellow" variant="light" leftSection={<Clock size={12} />}>
        {status === 'processing' ? 'Processing' : 'Pending'}
      </Badge>
    )
  }
  if (status === 'failed' || status === 'error') {
    return (
      <Badge color="red" variant="light" leftSection={<XCircle size={12} />}>
        Failed
      </Badge>
    )
  }
  return <Badge color="gray" variant="light">{status}</Badge>
}
