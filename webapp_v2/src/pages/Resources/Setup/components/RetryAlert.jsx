import { Stack, Text } from '@mantine/core'
import { AlertCircle } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'

export default function RetryAlert({ title, text, onRetry }) {
  return (
    <Alert icon={<AlertCircle size={16} />} color="red" title={title}>
      <Stack gap="sm" align="flex-start">
        <Text size="sm">{text}</Text>
        <Button variant="light" size="xs" onClick={onRetry}>
          Try again
        </Button>
      </Stack>
    </Alert>
  )
}
