import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Box, Group, Loader, Text } from '@mantine/core'
import { AnsiHtml } from 'fancy-ansi/react'
import classes from './LogsContainer.module.css'

/**
 * Port of `virtualized-container` (webapp/src/webapp/components/logs_container.cljs:78-108)
 * together with the list engine it delegates to
 * (webapp/src/webapp/components/virtualized_list.cljs).
 *
 * Renders a large log string without putting every line in the DOM: only the
 * rows inside the scroll window (plus an overscan margin) are mounted, so a
 * 500k-line result scrolls as smoothly as a 10-line one.
 *
 * The container fills its parent — place it inside a cell with a bounded
 * height (e.g. a flex child with `flex={1}` and `mih={0}`).
 */

/**
 * Fixed row height. Virtualization maps `scrollTop` to an index by dividing by
 * this number, so every row must be exactly this tall — the row carries an
 * explicit height rather than relying on its content, otherwise a blank line
 * would collapse and drag the rows under it out of alignment.
 *
 * v1 used `leading-5` (1.25rem); Mantine's size props convert 20 to the same
 * 1.25rem, so the two render identically.
 */
const LINE_HEIGHT = 20

/** Extra rows rendered above and below the window (virtualized_list.cljs:51). */
const OVERSCAN = 5

/**
 * Splits the raw log payload the same way Clojure's `string/split` does:
 * trailing empty segments are dropped, so a payload ending in newlines does
 * not add blank rows (and phantom scroll height) at the bottom.
 */
function toLines(logs) {
  if (!logs) return []
  const lines = logs.split('\n')
  while (lines.length > 0 && lines[lines.length - 1] === '') lines.pop()
  return lines
}

/**
 * Everything that is not a rendered log: the loading row, the failure copy and
 * the empty state. Port of the `logs-area` multimethod (logs_container.cljs:14-22).
 */
function LogsStatusMessage({ status }) {
  if (status === 'loading') {
    return (
      <Group gap="xs" align="center">
        <Text fz="sm">loading</Text>
        <Loader size={16} color="gray.2" />
      </Group>
    )
  }

  if (status === 'error' || status === 'failure') {
    return <Text fz="sm">There was an error to get the logs for this task</Text>
  }

  return <Text fz="sm">No logs to show</Text>
}

/**
 * The windowing engine. Measures itself with a ResizeObserver so the visible
 * window matches the area actually rendered, rather than a height passed in by
 * the caller (virtualized_list.cljs:23-38).
 */
function VirtualizedLines({ lines }) {
  const viewportRef = useRef(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [viewportHeight, setViewportHeight] = useState(0)

  useLayoutEffect(() => {
    const el = viewportRef.current
    if (!el) return undefined

    setViewportHeight(el.clientHeight)

    if (typeof ResizeObserver === 'undefined') return undefined
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0]
      if (entry) setViewportHeight(entry.contentRect.height)
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  // New payload → back to the top. v1 reset only its scroll-top atom and left
  // the element where it was, so the window went blank until the user touched
  // the wheel. Scrolling the element instead is the one source of truth: the
  // resulting scroll event feeds `scrollTop` back in, so DOM and state can
  // never disagree.
  useLayoutEffect(() => {
    const el = viewportRef.current
    if (el) el.scrollTop = 0
  }, [lines])

  const total = lines.length
  const visibleCount = Math.ceil(viewportHeight / LINE_HEIGHT)
  const startIndex = Math.max(0, Math.floor(scrollTop / LINE_HEIGHT) - OVERSCAN)
  const endIndex = Math.min(total, startIndex + visibleCount + OVERSCAN * 2)

  const safeStart = Math.max(0, Math.min(startIndex, Math.max(0, total - 1)))
  const safeEnd = Math.max(safeStart, Math.min(endIndex, total))
  const visibleLines = total > 0 && safeStart < total ? lines.slice(safeStart, safeEnd) : []

  return (
    <Box
      ref={viewportRef}
      className={classes.viewport}
      pos="relative"
      h="100%"
      w="100%"
      onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
    >
      {/* Spacer that gives the scrollbar the full length of the log. */}
      <Box pos="relative" h={total * LINE_HEIGHT}>
        <Box pos="absolute" top={safeStart * LINE_HEIGHT} left={0} right={0}>
          {visibleLines.map((line, index) => (
            <Box key={safeStart + index} className={classes.line} h={LINE_HEIGHT}>
              {/* Terminal output carries ANSI escape codes; fancy-ansi turns
                  them into styled spans instead of printing the raw bytes. */}
              <AnsiHtml text={line} />
            </Box>
          ))}
        </Box>
      </Box>
    </Box>
  )
}

/**
 * @param {object} props
 * @param {string} [props.status] `'success'` renders the logs, `'loading'` the
 *   spinner row, `'error'`/`'failure'` the failure copy; anything else
 *   (`'idle'`, `undefined`) falls back to "No logs to show". This is v1's
 *   vocabulary (logs_container.cljs:15,21) — `:success`/`:loading`/`:failure`,
 *   which is also what the results pipeline computes. Do NOT swap it for the
 *   store's `ready`: the two meet here and a mismatch silently blanks
 *   vocabulary.
 * @param {string} [props.logs] The raw log payload.
 * @param {string} [props.className] Extra class for the outer surface — v1's
 *   `:classes` config key.
 */
export default function LogsContainer({ status, logs, className }) {
  const lines = useMemo(() => toLines(logs), [logs])

  return (
    <Box
      className={[classes.root, className].filter(Boolean).join(' ')}
      pos="relative"
      h="100%"
      p="md"
      ff="monospace"
      fz="sm"
    >
      {status === 'success' ? (
        <VirtualizedLines lines={lines} />
      ) : (
        <LogsStatusMessage status={status} />
      )}
    </Box>
  )
}
