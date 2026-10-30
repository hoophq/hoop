import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Box, Group, Loader, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import { sessionsService } from '@/services/sessions'
import EntityLegend from './EntityLegend'
import PlaybackControls from './PlaybackControls'
import RdpCanvas from './RdpCanvas'
import {
  entityTypesOf,
  frameIndexAt,
  groupDetectionsBySnapshot,
  mergeEnabledTypes,
  needsMoreFrames,
} from './rdpLogic'
import { loadRleDecoder } from './rleDecoder'
import { useRdpAnalysis } from './useRdpAnalysis'
import classes from './Rdp.module.css'

const FIRST_PAGE = 100
const NEXT_PAGE = 200
// Below this, the clock and the overlay do not need a new render.
const TIME_STEP = 0.1

const messageOf = (err) => err?.response?.data?.message || err?.message || 'Request failed.'

// Plays the bitmaps of an RDP recording by their timestamps. Frames load in
// pages and always from the first one: a frame only shows right on top of
// everything drawn before it.
export default function RdpStreamingPlayer({ sessionId, width, height }) {
  const [attempt, setAttempt] = useState(0)
  const [status, setStatus] = useState('loading') // loading | ready | error
  const [error, setError] = useState(null)
  const [frames, setFrames] = useState([])
  const [totals, setTotals] = useState({ frames: 0, duration: 0 })
  const [fetching, setFetching] = useState(false)
  const [moreError, setMoreError] = useState(null)
  const [index, setIndex] = useState(0)
  const [time, setTime] = useState(0)
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState(1)
  const [fullscreen, setFullscreen] = useState(false)
  const [enabledTypes, setEnabledTypes] = useState(() => new Set())
  const containerRef = useRef(null)
  // The playback loop reads these between renders.
  const live = useRef({
    frames: [],
    total: 0,
    duration: 0,
    index: 0,
    time: 0,
    playing: false,
    speed: 1,
    startPerf: 0,
    startOffset: 0,
    raf: 0,
    fetching: false,
    moreFailed: false,
    mounted: true,
  })

  const { loading: detectionsLoading, analysis, detections, retrying, retry } = useRdpAnalysis(
    status === 'ready' ? sessionId : null,
  )
  const types = useMemo(() => entityTypesOf(detections), [detections])
  const snapshots = useMemo(() => groupDetectionsBySnapshot(detections), [detections])
  const previousTypes = useRef([])
  useEffect(() => {
    // Read the previous types now: the updater runs later, after the ref moved.
    const previous = previousTypes.current
    previousTypes.current = types
    setEnabledTypes((enabled) => mergeEnabledTypes(enabled, previous, types))
  }, [types])

  const moveTo = useCallback((nextIndex, nextTime) => {
    const l = live.current
    l.index = nextIndex
    l.time = nextTime
    setIndex(nextIndex)
    setTime(nextTime)
  }, [])

  const stopLoop = useCallback(() => {
    const l = live.current
    l.playing = false
    cancelAnimationFrame(l.raf)
    setPlaying(false)
  }, [])

  // The first page, after the bitmap decoder: a frame drawn before it would be
  // decoded as raw pixels.
  useEffect(() => {
    let cancelled = false
    const l = live.current
    l.mounted = true
    setStatus('loading')
    setError(null)
    Promise.all([loadRleDecoder(), sessionsService.rdpFrames(sessionId, { offset: 0, limit: FIRST_PAGE })])
      .then(([, { data }]) => {
        if (cancelled) return
        const page = data?.frames || []
        l.frames = page
        l.total = Math.max(data?.total_frames || 0, page.length)
        l.duration = data?.total_duration || page[page.length - 1]?.timestamp || 0
        l.moreFailed = false
        setFrames(page)
        setTotals({ frames: l.total, duration: l.duration })
        setMoreError(null)
        moveTo(0, page[0]?.timestamp || 0)
        setStatus('ready')
      })
      .catch((err) => {
        if (cancelled) return
        setError(messageOf(err))
        setStatus('error')
      })
    return () => {
      cancelled = true
    }
  }, [sessionId, attempt, moveTo])

  useEffect(
    () => () => {
      live.current.mounted = false
      cancelAnimationFrame(live.current.raf)
    },
    [],
  )

  useEffect(() => {
    const onChange = () => setFullscreen(document.fullscreenElement === containerRef.current)
    document.addEventListener('fullscreenchange', onChange)
    return () => document.removeEventListener('fullscreenchange', onChange)
  }, [])

  // Appends the next page. False when nothing more can load: all is loaded, a
  // page is on its way, or the last one failed (until the user retries).
  const loadMore = useCallback(async () => {
    const l = live.current
    if (l.fetching || l.moreFailed || l.frames.length >= l.total) return false
    l.fetching = true
    setFetching(true)
    try {
      const { data } = await sessionsService.rdpFrames(sessionId, { offset: l.frames.length, limit: NEXT_PAGE })
      if (!l.mounted) return false
      const page = data?.frames || []
      if (!page.length) {
        // The gateway has fewer frames than it counted: stop asking.
        l.total = l.frames.length
        setTotals((t) => ({ ...t, frames: l.total }))
        return false
      }
      l.frames = [...l.frames, ...page]
      setFrames(l.frames)
      return true
    } catch (err) {
      if (!l.mounted) return false
      l.moreFailed = true
      setMoreError(messageOf(err))
      return false
    } finally {
      l.fetching = false
      if (l.mounted) setFetching(false)
    }
  }, [sessionId])

  const tick = useCallback(() => {
    const l = live.current
    if (!l.playing || !l.mounted) return
    const t = l.startOffset + ((performance.now() - l.startPerf) * l.speed) / 1000
    if (t >= l.duration) {
      // The end: the last frame, stopped.
      stopLoop()
      moveTo(Math.max(0, l.frames.length - 1), l.duration)
      return
    }
    const nextIndex = frameIndexAt(l.frames, t)
    if (nextIndex !== l.index) {
      l.index = nextIndex
      setIndex(nextIndex)
    }
    if (Math.abs(t - l.time) >= TIME_STEP) {
      l.time = t
      setTime(t)
    }
    if (needsMoreFrames(nextIndex, l.frames.length, l.total)) loadMore()
    l.raf = requestAnimationFrame(tick)
  }, [loadMore, moveTo, stopLoop])

  const startLoop = useCallback(
    (from) => {
      const l = live.current
      l.playing = true
      l.startOffset = from
      l.startPerf = performance.now()
      setPlaying(true)
      cancelAnimationFrame(l.raf)
      l.raf = requestAnimationFrame(tick)
    },
    [tick],
  )

  // The current position while the loop runs, frozen when it stops.
  const now = () => {
    const l = live.current
    return l.playing ? l.startOffset + ((performance.now() - l.startPerf) * l.speed) / 1000 : l.time
  }

  const play = () => {
    const l = live.current
    // Play at the end starts over.
    const from = l.time >= l.duration ? 0 : l.time
    if (from === 0) moveTo(0, 0)
    startLoop(from)
  }

  const pause = () => {
    const t = Math.min(now(), live.current.duration)
    stopLoop()
    moveTo(frameIndexAt(live.current.frames, t), t)
  }

  // Frame steps pause first: the loop would move the frame right back.
  const prev = () => {
    stopLoop()
    const l = live.current
    const target = Math.max(0, l.index - 1)
    moveTo(target, l.frames[target]?.timestamp ?? 0)
  }

  const next = async () => {
    stopLoop()
    const l = live.current
    if (l.index + 1 >= l.frames.length) await loadMore()
    const target = Math.min(l.index + 1, l.frames.length - 1)
    moveTo(target, l.frames[target]?.timestamp ?? l.time)
  }

  // Seeking past the loaded frames loads every page up to the target first, so
  // the frames stay complete from the first one.
  const seek = async (target) => {
    const l = live.current
    const wasPlaying = l.playing
    if (wasPlaying) stopLoop()
    const lastLoaded = () => l.frames[l.frames.length - 1]?.timestamp ?? 0
    while (lastLoaded() < target && l.frames.length < l.total) {
      if (!(await loadMore())) break
    }
    if (!l.mounted) return
    moveTo(frameIndexAt(l.frames, target), target)
    if (wasPlaying && target < l.duration) startLoop(target)
  }

  // A new speed applies from the current position, not from the last Play.
  const changeSpeed = (value) => {
    const l = live.current
    if (l.playing) {
      l.startOffset = now()
      l.startPerf = performance.now()
    }
    l.speed = value
    setSpeed(value)
  }

  const toggleFullscreen = () => {
    if (document.fullscreenElement) document.exitFullscreen?.()
    else containerRef.current?.requestFullscreen?.()
  }

  const retryMore = () => {
    live.current.moreFailed = false
    setMoreError(null)
    loadMore()
  }

  const toggleType = (type) =>
    setEnabledTypes((enabled) => {
      const nextTypes = new Set(enabled)
      if (nextTypes.has(type)) nextTypes.delete(type)
      else nextTypes.add(type)
      return nextTypes
    })

  if (status === 'loading') {
    return (
      <Group justify="center" gap="xs" py="xl">
        <Loader size="xs" />
        <Badge variant="light" color="blue" tag>
          Loading frames...
        </Badge>
      </Group>
    )
  }

  if (status === 'error') {
    return (
      <Alert color="red" title="The recording could not be loaded">
        <Group justify="space-between" wrap="nowrap">
          <Text size="sm">{error}</Text>
          <Button size="xs" variant="light" color="red" onClick={() => setAttempt((n) => n + 1)}>
            Try again
          </Button>
        </Group>
      </Alert>
    )
  }

  if (!frames.length) {
    return (
      <Alert color="gray" title="Nothing to replay">
        The gateway returned no frames for this recording.
      </Alert>
    )
  }

  return (
    <Box ref={containerRef} className={classes.player} data-fullscreen={fullscreen || undefined}>
      <RdpCanvas
        frames={frames}
        index={index}
        time={time}
        width={width}
        height={height}
        snapshots={snapshots}
        enabledTypes={enabledTypes}
      >
        {fetching && (
          <Badge variant="light" color="blue" tag className={classes.stageBadge}>
            Loading more frames...
          </Badge>
        )}
      </RdpCanvas>
      <PlaybackControls
        time={time}
        duration={totals.duration}
        playing={playing}
        speed={speed}
        fullscreen={fullscreen}
        onPlay={play}
        onPause={pause}
        onPrev={prev}
        onNext={next}
        onSeek={seek}
        onSpeed={changeSpeed}
        onFullscreen={toggleFullscreen}
      />
      {moreError && (
        <Alert color="red" radius={0}>
          <Group justify="space-between" wrap="nowrap">
            <Text size="sm">{`Failed to load more frames: ${moreError}`}</Text>
            <Button size="xs" variant="light" color="red" onClick={retryMore}>
              Try again
            </Button>
          </Group>
        </Alert>
      )}
      <EntityLegend
        loading={detectionsLoading}
        analysis={analysis}
        retrying={retrying}
        onRetry={retry}
        types={types}
        enabledTypes={enabledTypes}
        onToggle={toggleType}
      />
    </Box>
  )
}
