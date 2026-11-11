import { Fragment } from 'react'
import { Link } from 'react-router-dom'
import { Anchor, Box, Divider, Group, Input, Paper, Stack, Text } from '@mantine/core'
import Code from '@/components/Code'
import MultiSelect from '@/components/MultiSelect'
import SegmentedControl from '@/components/SegmentedControl'
import Switch from '@/components/Switch'
import { useAiSessionAnalyzerStore } from '../../store'
import {
  DELIVERIES,
  NOTIFY_INTEGRATIONS,
  ROUTING_MODES,
  ROUTING_STATIC,
  deliveryUsesChannels,
  isKnownDelivery,
  notifyRouteToSpec,
} from '../approvalRouting'
import classes from '../Create.module.css'
import ApprovalGroups from './ApprovalGroups'

// The Slack page is the integrations page every gateway has.
const INTEGRATIONS_PATH = '/integrations/slack'

const helper = (text) => (
  <Text size="xs" c="dimmed">
    {text}
  </Text>
)

// The integration's channels plus the stored ids it does not list, so they
// can be unselected.
function channelOptions(channels, stored) {
  const known = new Set(channels.map((c) => c.id))
  return [
    ...channels.map((c) => ({ value: c.id, label: c.name })),
    ...stored.filter((id) => !known.has(id)).map((id) => ({ value: id, label: id })),
  ]
}

// One integration row: a switch and, when on, how it notifies.
function NotifyRow({ label, route, channels, onChange }) {
  return (
    <Stack gap="sm" p="md">
      <Group justify="space-between" wrap="nowrap">
        <Text size="sm" fw={500}>
          {label}
        </Text>
        <Switch
          aria-label={`Notify through ${label}`}
          checked={route.on}
          onChange={(e) => onChange({ on: e.currentTarget.checked })}
        />
      </Group>
      {route.on && (
        <Stack gap="sm">
          <Input.Wrapper label="Delivery">
            <Box mt={4}>
              <SegmentedControl
                w="fit-content"
                size="xs"
                data={DELIVERIES}
                value={isKnownDelivery(route.delivery) ? route.delivery : null}
                onChange={(delivery) => onChange({ delivery })}
              />
            </Box>
          </Input.Wrapper>
          {!isKnownDelivery(route.delivery) &&
            helper(`The stored delivery "${route.delivery}" is not known: the sidecar uses Direct message. It is kept until you select one.`)}
          {deliveryUsesChannels(route.delivery) && (
            <Stack gap={6}>
              <MultiSelect
                label="Candidate channels"
                placeholder="All available channels"
                data={channelOptions(channels, route.channels)}
                value={route.channels}
                onChange={(next) => onChange({ channels: next })}
                searchable
                clearable
              />
              {helper('The AI selects one or more. Empty: all available channels.')}
            </Stack>
          )}
        </Stack>
      )}
    </Stack>
  )
}

// The notify entries this form does not edit: keys the plane does not read,
// and integrations that are not set up. A save keeps them as stored.
function KeptEntries({ entries }) {
  if (entries.length === 0) return null
  return (
    <Stack gap={4} px="md" pb="md">
      {helper('Kept as stored. This form does not edit them.')}
      {entries.map(([key, value]) => (
        <Code key={key}>{`${key}: ${JSON.stringify(value ?? {})}`}</Code>
      ))}
    </Stack>
  )
}

function Notify({ routing, onRoute }) {
  const integrations = useAiSessionAnalyzerStore((s) => s.routingIntegrations)
  const status = useAiSessionAnalyzerStore((s) => s.routingIntegrationsStatus)
  const loaded = status === 'success'
  const rows = loaded ? NOTIFY_INTEGRATIONS.filter(({ key }) => integrations[key].integrated) : []
  // An entry for an integration that is not set up stays as stored.
  const kept = [
    ...NOTIFY_INTEGRATIONS.filter(({ key }) => routing.notify[key].on && !rows.some((r) => r.key === key)).map(
      ({ key }) => [key, notifyRouteToSpec(routing.notify[key])],
    ),
    ...Object.entries(routing.otherNotify),
  ]
  const anyOn = rows.some(({ key }) => routing.notify[key].on)

  let body
  if (status === 'error') {
    body = (
      <Text size="sm" c="red" p="md">
        Failed to load the integrations. Stored notify entries are kept.
      </Text>
    )
  } else if (!loaded) {
    body = (
      <Text size="sm" c="dimmed" p="md">
        Loading integrations...
      </Text>
    )
  } else if (rows.length === 0) {
    body = (
      <Text size="sm" c="dimmed" p="md">
        {'No integration set up. Set one up in '}
        <Anchor component={Link} to={INTEGRATIONS_PATH} size="sm">
          Integrations
        </Anchor>
        {'.'}
      </Text>
    )
  } else {
    body = rows.map(({ key, label }, index) => (
      <Fragment key={key}>
        {index > 0 && <Divider />}
        <NotifyRow
          label={label}
          route={routing.notify[key]}
          channels={integrations[key].channels}
          onChange={(patch) => onRoute(key, patch)}
        />
      </Fragment>
    ))
  }

  return (
    <Stack gap={6}>
      <Paper withBorder radius="lg" className={classes.clipped}>
        <Stack gap={2} px="md" py="sm" bg="var(--hoop-surface-muted)">
          <Text size="sm" fw={600}>
            Notify
          </Text>
          <Text size="xs" c="dimmed">
            Select the integrations that notify the approvers the AI selects.
          </Text>
        </Stack>
        <Divider />
        {body}
        <KeptEntries entries={kept} />
      </Paper>
      {loaded && rows.length > 0 && !anyOn && helper("Notifications follow today's behavior.")}
    </Stack>
  )
}

// How a human level's approval request finds its approvers (ENG-663): the
// static groups, or an AI agent that selects groups, people and channels.
export default function ApprovalRouting({ approval, groupOptions, onChange }) {
  const { routing } = approval
  const setRouting = (patch) => onChange({ routing: { ...routing, ...patch } })
  const setRoute = (key, patch) =>
    setRouting({ notify: { ...routing.notify, [key]: { ...routing.notify[key], ...patch } } })

  return (
    <Stack gap="md">
      <Input.Wrapper label="Routing">
        <Box mt={4}>
          <SegmentedControl
            w="fit-content"
            data={ROUTING_MODES}
            value={routing.mode}
            onChange={(mode) => setRouting({ mode })}
          />
        </Box>
      </Input.Wrapper>
      {routing.mode === ROUTING_STATIC ? (
        <ApprovalGroups options={groupOptions} value={approval.groups} onChange={(groups) => onChange({ groups })} />
      ) : (
        <>
          <Stack gap={6}>
            <MultiSelect
              label="Candidate groups"
              placeholder="All groups"
              data={groupOptions}
              value={routing.candidates}
              onChange={(candidates) => setRouting({ candidates })}
              searchable
              clearable
            />
            {helper('The AI selects one or more of these groups. Empty: all groups.')}
          </Stack>
          <Notify routing={routing} onRoute={setRoute} />
        </>
      )}
    </Stack>
  )
}
