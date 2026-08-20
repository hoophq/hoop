import { Link } from 'react-router-dom'
import { Badge, Button, Group, Stack, Text, ThemeIcon, Tooltip } from '@mantine/core'
import { STATUS_META, catalogDocsUrl } from '../constants'

/**
 * Status icon with the evaluated message + evidence in a tooltip. The row
 * itself stays scannable; the "why" lives here. The icon is focusable and
 * labeled so the tooltip content is reachable by keyboard, touch, and screen
 * readers — it is the only place framework rows expose the message.
 */
export function StatusIndicator({ status, message, evidence, size = 18 }) {
  const meta = STATUS_META[status] ?? STATUS_META.unable_to_verify
  const Icon = meta.icon
  return (
    <Tooltip
      multiline
      w={320}
      withArrow
      events={{ hover: true, focus: true, touch: true }}
      label={
        <Stack gap={4}>
          <Text size="xs" fw={600}>
            {meta.label}
          </Text>
          {message && <Text size="xs">{message}</Text>}
          {evidence && (
            <Text size="xs" c="dimmed">
              {evidence}
            </Text>
          )}
        </Stack>
      }
    >
      <ThemeIcon
        variant="light"
        color={meta.color}
        size={26}
        radius="xl"
        tabIndex={0}
        aria-label={message ? `${meta.label}: ${message}` : meta.label}
      >
        <Icon size={size} />
      </ThemeIcon>
    </Tooltip>
  )
}

/**
 * Remediation action for a control row. "app" navigates in-app, "docs" opens
 * the public documentation, "external" renders a passive hint (the fix lives
 * in the customer's IdP/infrastructure, there is nothing to open).
 */
export function ActionLink({ action }) {
  if (!action) return null
  if (action.type === 'app') {
    return (
      <Button component={Link} to={action.target} variant="subtle" size="compact-sm">
        {action.label}
      </Button>
    )
  }
  if (action.type === 'docs') {
    return (
      <Button
        component="a"
        href={catalogDocsUrl(action.target)}
        target="_blank"
        rel="noopener noreferrer"
        variant="subtle"
        size="compact-sm"
      >
        {action.label}
      </Button>
    )
  }
  return (
    <Text size="sm" c="dimmed" style={{ whiteSpace: 'nowrap' }}>
      {action.label}
    </Text>
  )
}

/** One control requirement row: status, ID, name, description, action. */
export function ControlRow({ control }) {
  return (
    <Group justify="space-between" align="flex-start" wrap="nowrap" gap="md">
      <Group align="flex-start" wrap="nowrap" gap="sm">
        <StatusIndicator
          status={control.status}
          message={control.message}
          evidence={control.evidence}
        />
        <Stack gap={2}>
          <Group gap="xs" wrap="nowrap">
            <Badge variant="default" size="sm" radius="sm" ff="monospace" tt="none">
              {control.id}
            </Badge>
            <Text size="sm" fw={500}>
              {control.title}
            </Text>
          </Group>
          <Text size="sm" c="dimmed">
            {control.description}
          </Text>
        </Stack>
      </Group>
      <ActionLink action={control.action} />
    </Group>
  )
}
