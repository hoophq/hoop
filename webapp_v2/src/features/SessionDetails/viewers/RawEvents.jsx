import { useMemo, useState } from 'react'
import { Box, Group, Paper, Stack, Text, UnstyledButton } from '@mantine/core'
import { ChevronDown, ChevronUp, Search } from 'lucide-react'
import TextInput from '@/components/TextInput'
import { formatFullDate } from '@/utils/datetime'
import { deniedAt } from '../decoders/liveTail'
import { b64ToText } from '../decoders/terminalDecoder'
import DeniedBadge from './recording/DeniedBadge'
import EmptyStream from './recording/EmptyStream'
import LargeRecording from './recording/LargeRecording'
import classes from './recording/recording.module.css'

const NO_DENIALS = new Map()

function RawRow({ row }) {
  const [open, setOpen] = useState(false)
  return (
    <Box className={classes.row} data-type={row.type}>
      <UnstyledButton
        className={classes.rowHead}
        w="100%"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <Group gap="sm" wrap="nowrap">
          {row.deniedRules && <DeniedBadge rules={row.deniedRules} />}
          <Text size="xs" className={classes.mono} truncate flex={1} c={row.type === 'e' ? 'red.9' : undefined}>
            {row.type === 'i' ? `> ${row.text}` : row.text}
          </Text>
          <Text size="xs" c="dimmed" flex="none">
            {row.date}
          </Text>
          {open ? <ChevronUp size={16} aria-hidden /> : <ChevronDown size={16} aria-hidden />}
        </Group>
      </UnstyledButton>
      {open && <div className={classes.rowContent}>{row.text}</div>}
    </Box>
  )
}

// The events of a session one row each: input, output and errors, with the
// time each happened. A guardrail's denial marks the statement it stopped.
// `showDenials` is off for the raw view of an SSH recording.
export default function RawEvents({ session, largeOutput, showDenials = true }) {
  const [search, setSearch] = useState('')
  const stream = useMemo(() => session.event_stream || [], [session.event_stream])
  const denied = useMemo(
    () => (showDenials ? deniedAt(session.guardrails_info) : NO_DENIALS),
    [showDenials, session.guardrails_info],
  )
  const rows = useMemo(() => {
    const start = new Date(session.start_date).getTime()
    return stream.map(([seconds, type, payload], index) => ({
      key: index,
      type,
      text: b64ToText(payload),
      date: formatFullDate(new Date(start + Math.trunc(seconds * 1000)).toISOString()),
      deniedRules: type !== 'e' ? denied.get(seconds) : undefined,
    }))
  }, [stream, session.start_date, denied])

  const term = search.trim().toLowerCase()
  const visible = term
    ? rows.filter((row) => row.text.toLowerCase().includes(term) || row.date.includes(term))
    : rows

  if (largeOutput) return <LargeRecording session={session} />
  if (!stream.length) return <EmptyStream />

  return (
    <Stack gap="sm">
      <TextInput
        maw={480}
        placeholder="Search content in the queries below"
        aria-label="Search the session events"
        leftSection={<Search size={16} />}
        value={search}
        onChange={(event) => setSearch(event.currentTarget.value)}
      />
      <Paper withBorder radius="md">
        {visible.length ? (
          visible.map((row) => <RawRow key={row.key} row={row} />)
        ) : (
          <Text size="sm" c="dimmed" p="md">
            No event matches this search.
          </Text>
        )}
      </Paper>
    </Stack>
  )
}
