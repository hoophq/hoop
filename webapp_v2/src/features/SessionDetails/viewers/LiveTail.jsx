import { useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { Box, Group, Stack, Text } from '@mantine/core'
import { AnsiHtml } from 'fancy-ansi/react'
import { ArrowDown, Database, Search, Terminal, Zap } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Badge from '@/components/Badge'
import Switch from '@/components/Switch'
import TextInput from '@/components/TextInput'
import Tooltip from '@/components/Tooltip'
import { getTick, subscribeTick } from '@/utils/tick'
import {
  deniedAt,
  durationBetween,
  formatAbsolute,
  formatRelative,
  liveTerminalEvents,
  streamRows,
} from '../decoders/liveTail'
import { recordingFormat } from '../decoders/sessionFormat'
import { terminalText } from '../decoders/terminalDecoder'
import DeniedBadge from './recording/DeniedBadge'
import EmptyStream from './recording/EmptyStream'
import { useSessionStream } from './recording/useSessionStream'
import classes from './recording/liveTail.module.css'

// Scrolled more than this above the end, the tail stops following.
const FOLLOW_THRESHOLD_PX = 32

const PILLS = {
  live: { color: 'green', label: 'Live', pulse: true },
  connecting: { color: 'amber', label: 'Connecting', pulse: true },
  ended: { color: 'gray', label: 'Ended' },
  error: { color: 'red', label: 'Stream lost' },
}

const noop = () => () => {}

function StatusPill({ state }) {
  const pill = PILLS[state]
  return (
    <Badge
      color={pill.color}
      variant="light"
      tag
      aria-label={`Stream status: ${pill.label}`}
      icon={
        <Box
          component="span"
          bg={`${pill.color}.6`}
          className={`${classes.dot} ${pill.pulse ? classes.pulse : ''}`}
          aria-hidden
        />
      }
    >
      {pill.label}
    </Badge>
  )
}

const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`

function Time({ row }) {
  return (
    <Tooltip label={row.absolute || ''} disabled={!row.absolute}>
      <Text size="xs" c="dimmed" fw={500} className={classes.time} pt={2}>
        {formatRelative(row.seconds)}
      </Text>
    </Tooltip>
  )
}

function QueryRow({ row }) {
  return (
    <Group gap="sm" align="flex-start" wrap="nowrap" className={classes.row}>
      <Time row={row} />
      {row.deniedRules ? (
        <DeniedBadge rules={row.deniedRules} />
      ) : (
        <Badge color="indigo" variant="light" tag flex="none" mt={2}>
          {row.pgTypeName}
        </Badge>
      )}
      <pre className={classes.pre}>{row.sql}</pre>
    </Group>
  )
}

function ProtocolRow({ row }) {
  return (
    <Group gap="sm" wrap="nowrap" className={classes.protocol}>
      <Time row={row} />
      <Group gap={6} wrap="nowrap">
        <Zap size={12} aria-hidden />
        <Text size="xs" fs="italic">
          {row.pgType ? `${row.pgTypeName} (${row.pgType})` : row.pgTypeName}
        </Text>
      </Group>
    </Group>
  )
}

const TEXT_COLORS = { o: undefined, e: 'red.9' }

function TextRow({ row }) {
  return (
    <Group gap="sm" align="flex-start" wrap="nowrap" className={classes.row}>
      <Time row={row} />
      {row.deniedRules && <DeniedBadge rules={row.deniedRules} />}
      <Text component="pre" className={classes.pre} c={row.type in TEXT_COLORS ? TEXT_COLORS[row.type] : 'dimmed'}>
        {row.text}
      </Text>
    </Group>
  )
}

function Waiting({ postgres }) {
  return (
    <Group justify="center" gap="xs" py="lgAlt" c="dimmed">
      <Box component="span" bg="green.6" className={`${classes.dot} ${classes.pulse}`} aria-hidden />
      <Text size="sm" fs="italic">
        {postgres ? 'Waiting for the next query…' : 'Waiting for events…'}
      </Text>
    </Group>
  )
}

// The events of an interactive connect session as they happen: a terminal for
// a pty, one row per event otherwise, and the SQL of each query for postgres.
// A finished postgres session keeps this view, ended.
export default function LiveTail({ session, largeOutput }) {
  const live = session.verb === 'connect' && session.status === 'open'
  const { state, events } = useSessionStream(session, { enabled: live, catchUp: !largeOutput })
  const postgres = session.connection_subtype === 'postgres'
  const format = recordingFormat(session)
  const [onlyQueries, setOnlyQueries] = useState(true)
  const [search, setSearch] = useState('')
  const [following, setFollowing] = useState(true)
  const scrollRef = useRef(null)
  // A ticking clock only while the elapsed time grows.
  const now = useSyncExternalStore(state === 'live' ? subscribeTick : noop, getTick)

  const denied = useMemo(() => deniedAt(session.guardrails_info), [session.guardrails_info])
  const rows = useMemo(() => streamRows(events, { postgres, denied }), [events, postgres, denied])
  const terminalEvents = useMemo(
    () => liveTerminalEvents(format, events, state === 'ended'),
    [format, events, state],
  )
  const terminal = terminalEvents !== null
  const text = terminal ? terminalText(terminalEvents) : ''
  const queryCount = rows.filter((row) => row.kind === 'query').length
  const hasProtocolRows = rows.some((row) => row.kind === 'protocol')
  const term = search.trim().toLowerCase()
  const visible = rows
    .filter((row) => !(postgres && onlyQueries) || row.kind === 'query')
    .filter(
      (row) =>
        !term || `${row.sql || ''} ${row.text || ''} ${row.pgTypeName || ''}`.toLowerCase().includes(term),
    )
    .map((row) => ({ ...row, absolute: formatAbsolute(session.start_date, row.seconds) }))

  const scrollToEnd = () => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }

  useLayoutEffect(() => {
    if (following) scrollToEnd()
  })

  const onScroll = (event) => {
    const el = event.currentTarget
    setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < FOLLOW_THRESHOLD_PX)
  }

  let body
  if (!rows.length && state === 'ended') body = <EmptyStream />
  else if (terminal) {
    body = text ? (
      <div className={classes.terminal}>
        <AnsiHtml text={text} />
      </div>
    ) : (
      <Waiting postgres={false} />
    )
  } else if (!visible.length) body = <Waiting postgres={postgres} />
  else {
    body = (
      <>
        {visible.map((row) => {
          if (row.kind === 'query') return <QueryRow key={row.key} row={row} />
          if (row.kind === 'protocol') return <ProtocolRow key={row.key} row={row} />
          return <TextRow key={row.key} row={row} />
        })}
        {state === 'live' && <Waiting postgres={postgres} />}
      </>
    )
  }

  const duration = durationBetween(session.start_date, session.end_date, now)

  return (
    <Stack gap={0}>
      <Group justify="space-between" gap="sm" className={classes.header}>
        <Group gap="md">
          <StatusPill state={state} />
          {postgres && (
            <Group gap={4} c="dimmed">
              <Database size={14} aria-hidden />
              <Text size="sm" fw={500}>
                {plural(queryCount, 'query', 'queries')}
              </Text>
            </Group>
          )}
          <Group gap={4} c="dimmed">
            <Terminal size={14} aria-hidden />
            <Text size="sm">{plural(rows.length, 'event', 'events')}</Text>
          </Group>
          {duration && (
            <Text size="sm" c="dimmed">
              {`· ${duration} ${state === 'ended' ? 'total' : 'elapsed'}`}
            </Text>
          )}
        </Group>
        <Group gap="md">
          {!terminal && (
            <TextInput
              size="sm"
              w={224}
              placeholder="Filter events…"
              aria-label="Filter live events"
              leftSection={<Search size={14} />}
              value={search}
              onChange={(event) => setSearch(event.currentTarget.value)}
            />
          )}
          {postgres && hasProtocolRows && (
            <Tooltip label="Hide protocol frames (Bind, Describe, Execute, Sync)">
              <div>
                <Switch
                  size="sm"
                  label="Only queries"
                  checked={onlyQueries}
                  onChange={(event) => setOnlyQueries(event.currentTarget.checked)}
                />
              </div>
            </Tooltip>
          )}
        </Group>
      </Group>
      <div ref={scrollRef} className={classes.body} onScroll={onScroll}>
        {body}
        {!following && state === 'live' && (
          <div className={classes.jump}>
            <ActionIcon
              size="lg"
              variant="filled"
              color="indigo"
              aria-label="Jump to latest"
              onClick={() => {
                setFollowing(true)
                scrollToEnd()
              }}
            >
              <ArrowDown size={18} />
            </ActionIcon>
          </div>
        )}
      </div>
    </Stack>
  )
}
