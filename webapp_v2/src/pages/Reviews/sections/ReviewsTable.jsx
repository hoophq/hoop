import { Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import Table from '@/components/Table'
import { formatRelativeTime } from '@/utils/datetime'
import { reviewSource, statusLabel } from '../helpers'
import classes from './ReviewsTable.module.css'

export default function ReviewsTable({ reviews, sidecarsById, selectedId, onSelect }) {
  return (
    <Table>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>Listener</Table.Th>
          <Table.Th>Rule</Table.Th>
          <Table.Th>Status</Table.Th>
          <Table.Th>Filed</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {reviews.map((review) => {
          const source = reviewSource(review, sidecarsById)
          const status = statusLabel(review.status)
          return (
            <Table.Tr
              key={review.id}
              onClick={() => onSelect(review)}
              className={classes.row}
              data-selected={review.id === selectedId || undefined}
            >
              <Table.Td miw={190}>
                <Stack gap={0}>
                  <Text size="sm" fw={600}>
                    {source.primary}
                  </Text>
                  {source.secondary && (
                    <Text size="xs" c="dimmed">
                      {source.secondary}
                    </Text>
                  )}
                </Stack>
              </Table.Td>
              <Table.Td miw={160}>
                <Text size="sm" c={review.access_request_rule_name ? undefined : 'dimmed'}>
                  {review.access_request_rule_name ?? '—'}
                </Text>
              </Table.Td>
              <Table.Td miw={120}>
                <Badge variant="light" color={status.color} fullLabel>
                  {status.label}
                </Badge>
              </Table.Td>
              <Table.Td miw={120}>
                <Text size="sm" c="dimmed">
                  {formatRelativeTime(review.created_at)}
                </Text>
              </Table.Td>
            </Table.Tr>
          )
        })}
      </Table.Tbody>
    </Table>
  )
}
