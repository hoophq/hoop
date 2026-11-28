import { Fragment } from 'react'
import { Link } from 'react-router-dom'
import { Anchor, Box, Combobox, Divider, Group, Image, Input, Paper, Stack, Text, ThemeIcon } from '@mantine/core'
import { Sparkles, UsersRound } from 'lucide-react'
import Badge from '@/components/Badge'
import Code from '@/components/Code'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import SegmentedControl from '@/components/SegmentedControl'
import Select from '@/components/Select'
import Switch from '@/components/Switch'
import Tooltip from '@/components/Tooltip'
import { useAiSessionAnalyzerStore } from '../../store'
import {
  DELIVERIES,
  DELIVERY_DM,
  NOTIFY_INTEGRATIONS,
  ROUTING_AI,
  ROUTING_MODES,
  ROUTING_STATIC,
  candidateSelection,
  deliveryUsesChannels,
  isKnownDelivery,
  isRoutingMode,
  notifyRouteToSpec,
  routingModePatch,
  selectCandidates,
} from '../approvalRouting'
import classes from '../Create.module.css'
import ApprovalGroups from './ApprovalGroups'

// The Slack page is the integrations page every gateway has.
const INTEGRATIONS_PATH = '/integrations/slack'
// Shared with the ClojureScript app (webapp/resources/public/images).
const SLACK_LOGO = '/images/Slack_icon_2019.svg'

const helper = (text) => (
  <Text size="xs" c="dimmed">
    {text}
  </Text>
)

const recommended = (
  <Badge variant="light" color="indigo" tag chip>
    Recommended
  </Badge>
)

// A mode, what it does and, for the AI, that it is the recommended one.
const renderMode = ({ option }) => (
  <Group gap="xs" wrap="nowrap" py={4} flex={1}>
    <Text size="sm">{option.label}</Text>
    <Text size="sm" c="dimmed" flex={1}>
      {option.description}
    </Text>
    {option.recommended && recommended}
  </Group>
)

// A channel reads as "#name"; a stored id the integration does not list
// stays an id, so it can be unselected.
const channelLabel = (name) => (name.startsWith('#') ? name : `#${name}`)
function channelOptions(channels, stored) {
  const known = new Set(channels.map((c) => c.id))
  return [
    ...channels.map((c) => ({ value: c.id, label: channelLabel(c.name) })),
    ...stored.filter((id) => !known.has(id)).map((id) => ({ value: id, label: id })),
  ]
}

const INTEGRATION_ICONS = {
  slack: <Image src={SLACK_LOGO} alt="" w={14} h={14} />,
  teams: <UsersRound size={14} aria-hidden="true" />,
}

// What the notify fields say in each mode. The AI selects the people and one
// or more of the channels; with groups nothing selects.
const NOTIFY_COPY = {
  [ROUTING_AI]: {
    delivery: { [DELIVERY_DM]: 'The AI sends a direct message to the people it selects.' },
    channelsLabel: 'Candidate channels',
    channelsPlaceholder: 'All available channels',
    channelsHelp: 'The AI selects one or more. Empty: all available channels',
  },
  [ROUTING_STATIC]: {
    delivery: {
      [DELIVERY_DM]: 'Every approver of the selected groups gets a direct message. No channel gets a post.',
      both: 'Every approver of the selected groups gets a direct message, and the channels get a post.',
    },
    channelsLabel: 'Channels',
    channelsPlaceholder: 'Channels of the listener',
    channelsHelp:
      'Every selected channel gets the post. With none selected, the channels of the listener get it, or the default channel of the organization.',
  },
}

// How one integration notifies, once it is on.
function NotifyFields({ route, channels, copy, onChange }) {
  return (
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
      {copy.delivery[route.delivery] && helper(copy.delivery[route.delivery])}
      {!isKnownDelivery(route.delivery) &&
        helper(`The stored delivery "${route.delivery}" is not known: the sidecar uses Direct message. It is kept until you select one.`)}
      {deliveryUsesChannels(route.delivery) && (
        <Stack gap={6}>
          <MultiSelect
            label={copy.channelsLabel}
            placeholder={copy.channelsPlaceholder}
            data={channelOptions(channels, route.channels)}
            value={route.channels}
            onChange={(next) => onChange({ channels: next })}
            searchable
            clearable
          />
          {helper(copy.channelsHelp)}
        </Stack>
      )}
    </Stack>
  )
}

// One integration row: its logo, a switch and, when on, how it notifies. An
// integration that is not set up keeps its switch off: Slack names its page,
// Teams waits for ENG-705.
function NotifyRow({ integration, integrated, route, channels, copy, onChange }) {
  const { key, label, description } = integration
  let note
  if (!integrated && key === 'slack') {
    note = (
      <Text size="xs" c="dimmed">
        {'Not set up. Set it up in '}
        <Anchor component={Link} to={INTEGRATIONS_PATH} size="xs">
          Integrations
        </Anchor>
        {'.'}
      </Text>
    )
  } else if (!integrated) {
    note = helper('Not integrated yet')
  } else {
    note = helper(description)
  }
  const control = (
    <Switch
      aria-label={`Notify through ${label}`}
      checked={integrated && route.on}
      disabled={!integrated}
      onChange={(e) => onChange({ on: e.currentTarget.checked })}
    />
  )
  return (
    <Stack gap="sm" p="md">
      <Group justify="space-between" wrap="nowrap" align="flex-start">
        <Group gap="sm" wrap="nowrap" align="flex-start">
          <ThemeIcon size={28} variant="default">
            {INTEGRATION_ICONS[key]}
          </ThemeIcon>
          <Stack gap={2}>
            <Text size="sm" fw={600}>
              {label}
            </Text>
            {note}
          </Stack>
        </Group>
        {integrated ? (
          control
        ) : (
          <Tooltip label={key === 'slack' ? 'Not set up' : 'Not integrated yet'}>
            <Box>{control}</Box>
          </Tooltip>
        )}
      </Group>
      {integrated && route.on && <NotifyFields route={route} channels={channels} copy={copy} onChange={onChange} />}
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

// The notify card of a human level. `routing.mode` picks the words and the
// shape of a kept entry: `routing.notify` of an AI-routed level, the level's
// own `notify` with groups.
function Notify({ routing, onRoute }) {
  const copy = NOTIFY_COPY[routing.mode]
  const explicit = routing.mode === ROUTING_STATIC
  const integrations = useAiSessionAnalyzerStore((s) => s.routingIntegrations)
  const status = useAiSessionAnalyzerStore((s) => s.routingIntegrationsStatus)
  const loaded = status === 'success'
  // An entry for an integration that is not set up stays as stored.
  const kept = [
    ...NOTIFY_INTEGRATIONS.filter(({ key }) => routing.notify[key].on && !integrations[key].integrated).map(
      ({ key }) => [key, notifyRouteToSpec(routing.notify[key], explicit)],
    ),
    ...Object.entries(routing.otherNotify),
  ]
  const anyOn = NOTIFY_INTEGRATIONS.some(({ key }) => integrations[key].integrated && routing.notify[key].on)

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
  } else {
    body = NOTIFY_INTEGRATIONS.map((integration, index) => (
      <Fragment key={integration.key}>
        {index > 0 && <Divider />}
        <NotifyRow
          integration={integration}
          integrated={integrations[integration.key].integrated}
          route={routing.notify[integration.key]}
          channels={integrations[integration.key].channels}
          copy={copy}
          onChange={(patch) => onRoute(integration.key, patch)}
        />
      </Fragment>
    ))
  }

  return (
    <Stack gap="sm">
      <Text size="sm" fw={600}>
        Notify
      </Text>
      <Stack gap={6}>
        <Paper withBorder className={classes.clipped}>
          {body}
          {(loaded || status === 'error') && <KeptEntries entries={kept} />}
        </Paper>
        {loaded && !anyOn && helper("Notifications follow today's behavior.")}
      </Stack>
    </Stack>
  )
}

// How a human level's approval request finds its approvers (ENG-663): an AI
// agent that selects groups, people and channels, or the static groups. Both
// say how the approvers are told.
export default function ApprovalRouting({ approval, groupOptions, groupsLoaded, onChange }) {
  const { routing } = approval
  const setRouting = (patch) => onChange({ routing: { ...routing, ...patch } })
  const setRoute = (key, patch) =>
    setRouting({ notify: { ...routing.notify, [key]: { ...routing.notify[key], ...patch } } })
  const ai = routing.mode === ROUTING_AI

  return (
    <Stack gap="md">
      <Select
        label="How approval is done"
        placeholder="Select a way to approve"
        data={ROUTING_MODES}
        renderOption={renderMode}
        value={isRoutingMode(routing.mode) ? routing.mode : null}
        onChange={(mode) => onChange(routingModePatch(approval, mode, groupOptions, groupsLoaded))}
        allowDeselect={false}
        rightSection={
          <Group gap="xs" wrap="nowrap" pr="xs">
            {ai && recommended}
            <Combobox.Chevron size="sm" />
          </Group>
        }
        rightSectionWidth="auto"
        rightSectionPointerEvents="none"
      />
      {ai ? (
        <>
          <Paper px="sm" py={8} bg="var(--hoop-tint-indigo)">
            <Group gap="xs" wrap="nowrap">
              <Sparkles size={14} aria-hidden="true" color="var(--mantine-primary-color-filled)" />
              <Text size="xs" c="indigo">
                AI selects the best groups from past approvals
              </Text>
            </Group>
          </Paper>
          <ApprovalGroups
            variant="candidates"
            options={groupOptions}
            value={candidateSelection(routing.candidates, groupOptions)}
            onChange={(selection) =>
              setRouting({ candidates: selectCandidates(selection, groupOptions, groupsLoaded) })
            }
          />
          <Notify routing={routing} onRoute={setRoute} />
        </>
      ) : (
        routing.mode === ROUTING_STATIC && (
          <>
            <Switch
              label="Sidecar owners can approve"
              description="Direct messages go to the Sidecar's owners. With no groups selected, only the owners can approve. The default Slack channel is never used."
              checked={Boolean(approval.owners)}
              onChange={(event) => onChange({ owners: event.currentTarget.checked, groupsChosen: true })}
            />
            <ApprovalGroups
              options={groupOptions}
              value={approval.groups}
              onChange={(groups) => onChange({ groups, groupsChosen: true })}
            />
            {((approval.owners ? 1 : 0) + (approval.groups?.length ?? 0) > 1 ||
              approval.allGroupsMustApprove ||
              Number(approval.minApprovals) > 1) && (
              <Stack gap="sm">
                <Switch
                  label="All groups must approve"
                  description="Every selected approver group (including Sidecar owners) must approve before the statement runs."
                  checked={Boolean(approval.allGroupsMustApprove)}
                  onChange={(event) =>
                    onChange({
                      allGroupsMustApprove: event.currentTarget.checked,
                      minApprovals: event.currentTarget.checked ? '' : approval.minApprovals,
                    })
                  }
                />
                {!approval.allGroupsMustApprove && (
                  <NumberInput
                    label="Minimum approvals"
                    description="Number of approver groups that must approve (1 when empty)."
                    min={1}
                    max={Math.max(1, (approval.owners ? 1 : 0) + (approval.groups?.length ?? 0))}
                    value={approval.minApprovals ?? ''}
                    onChange={(value) => onChange({ minApprovals: value === '' ? '' : Number(value) })}
                  />
                )}
              </Stack>
            )}
            <Notify routing={routing} onRoute={setRoute} />
          </>
        )
      )}
      {isRoutingMode(routing.mode) && (
        <Stack gap="sm">
          <Switch
            label="Escalate if unanswered"
            description="Add more groups and send their members a Slack direct message when nobody decides in time."
            checked={Boolean(approval.escalate?.on)}
            onChange={(event) =>
              onChange({
                escalate: {
                  afterMinutes: approval.escalate?.afterMinutes ?? 15,
                  groups: approval.escalate?.groups ?? [],
                  on: event.currentTarget.checked,
                },
              })
            }
          />
          {approval.escalate?.on && (
            <Stack gap="sm">
              <NumberInput
                label="Escalate after (minutes)"
                description="Between 1 minute and 7 days."
                min={1}
                max={10080}
                value={approval.escalate.afterMinutes}
                onChange={(value) =>
                  onChange({ escalate: { ...approval.escalate, afterMinutes: value === '' ? '' : Number(value) } })
                }
              />
              <MultiSelect
                label="Escalation groups"
                placeholder="Select groups"
                data={groupOptions}
                value={approval.escalate.groups}
                onChange={(groups) => onChange({ escalate: { ...approval.escalate, groups } })}
              />
            </Stack>
          )}
        </Stack>
      )}
    </Stack>
  )
}
