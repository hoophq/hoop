import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Group, Stack, Text, Title, Button, Indicator } from '@mantine/core'
import { Pause, Play, Trash2, Download } from 'lucide-react'
import Select from '@/components/Select'
import { serverLogsService } from '@/services/serverLogs'
import classes from './ServerLogs.module.css'

// Matches the gateway's in-memory ring capacity (gateway/serverlogs.Capacity);
// the server clamps larger requests to it anyway.
const BACKLOG = 500
const MAX_ENTRIES = 2000
const RECONNECT_DELAY_MS = 3000
const FLUSH_INTERVAL_MS = 100
const STICK_THRESHOLD_PX = 40

const LEVEL_CLASS = {
  debug: classes.levelDebug,
  info: classes.levelInfo,
  warn: classes.levelWarn,
  error: classes.levelError,
  dpanic: classes.levelError,
  panic: classes.levelError,
  fatal: classes.levelError,
}

// The "error" filter bucket covers zap's whole failure family.
const ERROR_FAMILY = new Set(['error', 'dpanic', 'panic', 'fatal'])

const LEVEL_OPTIONS = [
  { value: 'all', label: 'All levels' },
  { value: 'debug', label: 'Debug' },
  { value: 'info', label: 'Info' },
  { value: 'warn', label: 'Warn' },
  { value: 'error', label: 'Error' },
]

const SOURCE_OPTIONS = [
  { value: 'all', label: 'All sources' },
  { value: 'gateway', label: 'Gateway' },
  { value: 'agent', label: 'Agents' },
]

function matchesFilters(entry, level, source) {
  if (source !== 'all' && entry.source !== source) return false
  if (level === 'all') return true
  if (level === 'error') return ERROR_FAMILY.has(entry.level)
  return entry.level === level
}

function exportJSON(entries) {
  const payload = entries.map((e) => {
    const copy = { ...e }
    delete copy._id
    return copy
  })
  const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `server-logs-${new Date().toISOString().replace(/[:.]/g, '-')}.json`
  a.click()
  URL.revokeObjectURL(url)
}

function timeOf(ts) {
  const d = new Date(ts)
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function LogRow({ entry, expanded, onToggle }) {
  const isAgent = entry.source === 'agent'
  const fieldKeys = entry.fields ? Object.keys(entry.fields) : []
  return (
    <>
      <button
        type="button"
        className={classes.row}
        onClick={onToggle}
        aria-expanded={expanded}
        aria-label={`Log entry: ${entry.level} ${entry.message}`}
      >
        <span className={classes.time}>{timeOf(entry.timestamp)}</span>
        <span className={LEVEL_CLASS[entry.level] ?? classes.levelDebug}>
          {entry.level?.toUpperCase()}
        </span>
        <span className={isAgent ? classes.sourceAgent : classes.sourceGateway}>
          {isAgent ? (entry.agent_name || 'agent') : 'gateway'}
        </span>
        <span className={classes.message}>{entry.message}</span>
        {fieldKeys.length > 0 && (
          <span className={classes.fieldsHint}>+{fieldKeys.length} fields</span>
        )}
      </button>
      {expanded && (
        <div className={classes.details}>
          <div>
            <span className={classes.detailsLabel}>timestamp</span>
            <span className={classes.detailsValue}>{entry.timestamp}</span>
          </div>
          {entry.logger && (
            <div>
              <span className={classes.detailsLabel}>caller</span>
              <span className={classes.detailsValue}>{entry.logger}</span>
            </div>
          )}
          <div>
            <span className={classes.detailsLabel}>source</span>
            <span className={classes.detailsValue}>
              {isAgent ? `agent ${entry.agent_name || ''} (${entry.agent_id || 'unknown id'})` : 'gateway'}
            </span>
          </div>
          {fieldKeys.length > 0 && (
            <pre className={classes.detailsFields}>{JSON.stringify(entry.fields, null, 2)}</pre>
          )}
        </div>
      )}
    </>
  )
}

const STATUS = {
  connecting: { color: 'yellow', label: 'Connecting…' },
  live: { color: 'green', label: 'Live' },
  reconnecting: { color: 'red', label: 'Reconnecting…' },
  paused: { color: 'gray', label: 'Paused' },
}

export default function ServerLogs() {
  const [entries, setEntries] = useState([])
  const [status, setStatus] = useState('connecting')
  const [paused, setPaused] = useState(false)
  const [expandedIds, setExpandedIds] = useState(new Set())
  const [levelFilter, setLevelFilter] = useState('all')
  const [sourceFilter, setSourceFilter] = useState('all')

  const nextIdRef = useRef(1)
  const pendingRef = useRef([])
  const flushTimerRef = useRef(null)
  const scrollRef = useRef(null)
  const stickRef = useRef(true)

  // Entries arrive one per SSE frame; batch them so a 500-entry backlog
  // replay is one render, not five hundred.
  const push = useCallback((entry) => {
    pendingRef.current.push({ ...entry, _id: nextIdRef.current++ })
    if (flushTimerRef.current) return
    flushTimerRef.current = setTimeout(() => {
      flushTimerRef.current = null
      const batch = pendingRef.current
      pendingRef.current = []
      setEntries((prev) => {
        const next = [...prev, ...batch]
        return next.length > MAX_ENTRIES ? next.slice(next.length - MAX_ENTRIES) : next
      })
    }, FLUSH_INTERVAL_MS)
  }, [])

  useEffect(() => {
    if (paused) return undefined
    const controller = new AbortController()
    let retryTimer = null
    let cancelled = false

    async function connect() {
      setStatus('connecting')
      // Each (re)connect replays the backlog; reset so entries aren't duplicated.
      pendingRef.current = []
      setEntries([])
      setExpandedIds(new Set())
      try {
        await serverLogsService.stream({
          backlog: BACKLOG,
          signal: controller.signal,
          onOpen: () => setStatus('live'),
          onEntry: push,
        })
      } catch {
        // fallthrough to reconnect below unless aborted
      }
      if (cancelled || controller.signal.aborted) return
      setStatus('reconnecting')
      retryTimer = setTimeout(connect, RECONNECT_DELAY_MS)
    }

    connect()
    return () => {
      cancelled = true
      controller.abort()
      clearTimeout(retryTimer)
      if (flushTimerRef.current) {
        clearTimeout(flushTimerRef.current)
        flushTimerRef.current = null
      }
    }
  }, [paused, push])

  const filtered = useMemo(
    () => entries.filter((e) => matchesFilters(e, levelFilter, sourceFilter)),
    [entries, levelFilter, sourceFilter]
  )
  // Follow the tail unless the user scrolled up.
  useEffect(() => {
    const el = scrollRef.current
    if (el && stickRef.current) el.scrollTop = el.scrollHeight
  }, [filtered])

  function onScroll() {
    const el = scrollRef.current
    if (!el) return
    stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_THRESHOLD_PX
  }

  function toggleRow(id) {
    setExpandedIds((prev) => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
  }

  const statusInfo = STATUS[paused ? 'paused' : status]

  return (
    <Stack gap="lg" h="calc(100vh - 160px)">
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <Stack gap="xs">
          <Title order={1}>Server Logs</Title>
          <Text c="dimmed" size="lg">
            Runtime logs from the gateway and connected agents, streamed in real time.
          </Text>
        </Stack>
        <Group gap="sm">
          <Indicator color={statusInfo.color} processing={status === 'live'} position="middle-start" ml={12}>
            <Text size="sm" c="dimmed" ml="sm">
              {statusInfo.label}
            </Text>
          </Indicator>
          <Button
            variant="outline"
            color="gray"
            size="xs"
            leftSection={paused ? <Play size={14} /> : <Pause size={14} />}
            onClick={() => setPaused((p) => !p)}
          >
            {paused ? 'Resume' : 'Pause'}
          </Button>
          <Button
            variant="outline"
            color="gray"
            size="xs"
            leftSection={<Trash2 size={14} />}
            onClick={() => {
              pendingRef.current = []
              setEntries([])
              setExpandedIds(new Set())
            }}
          >
            Clear
          </Button>
        </Group>
      </Group>

      <Group gap="sm" justify="flex-end">
        <Select
          aria-label="Filter by level"
          data={LEVEL_OPTIONS}
          value={levelFilter}
          onChange={(v) => setLevelFilter(v ?? 'all')}
          size="xs"
          w={130}
        />
        <Select
          aria-label="Filter by source"
          data={SOURCE_OPTIONS}
          value={sourceFilter}
          onChange={(v) => setSourceFilter(v ?? 'all')}
          size="xs"
          w={130}
        />
        <Button
          variant="outline"
          color="gray"
          size="xs"
          leftSection={<Download size={14} />}
          disabled={filtered.length === 0}
          onClick={() => exportJSON(filtered)}
        >
          Export JSON
        </Button>
      </Group>

      <div ref={scrollRef} className={classes.terminal} onScroll={onScroll}>
        {filtered.length === 0 ? (
          <div className={classes.placeholder}>
            {entries.length > 0
              ? 'No entries match the current filters.'
              : status === 'live'
                ? 'Waiting for log entries…'
                : statusInfo.label}
          </div>
        ) : (
          filtered.map((entry) => (
            <LogRow
              key={entry._id}
              entry={entry}
              expanded={expandedIds.has(entry._id)}
              onToggle={() => toggleRow(entry._id)}
            />
          ))
        )}
      </div>
    </Stack>
  )
}
