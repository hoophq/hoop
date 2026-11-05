import { useEffect, useRef, useState } from 'react'
import { Box } from '@mantine/core'
import { LINE_HEIGHT, MAX_LINE_CHARS, humanizeSize, isClipped, virtualWindow } from './output'
import classes from './exec.module.css'

const OVERSCAN = 5

function Line({ line }) {
  if (!isClipped(line)) return <div className={classes.line}>{line}</div>
  return (
    <div className={classes.line}>
      {line.slice(0, MAX_LINE_CHARS)}
      <span className={classes.clipped}>{` … ${humanizeSize(line.length - MAX_LINE_CHARS)} clipped`}</span>
    </div>
  )
}

// Renders only the visible lines; the box measures itself.
export default function VirtualLines({ lines }) {
  const ref = useRef(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [height, setHeight] = useState(0)

  useEffect(() => {
    const el = ref.current
    if (!el) return undefined
    setHeight(el.clientHeight)
    const observer = new ResizeObserver(([entry]) => setHeight(entry.contentRect.height))
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  // A new output starts at the top.
  const [shownLines, setShownLines] = useState(lines)
  if (shownLines !== lines) {
    setShownLines(lines)
    setScrollTop(0)
  }
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = 0
  }, [lines])

  const { start, end, offsetY, totalHeight } = virtualWindow({
    scrollTop,
    height,
    itemHeight: LINE_HEIGHT,
    total: lines.length,
    overscan: OVERSCAN,
  })

  return (
    <div ref={ref} className={classes.scroller} onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}>
      <Box h={totalHeight} pos="relative">
        <Box pos="absolute" top={offsetY} left={0} right={0}>
          {lines.slice(start, end).map((line, i) => (
            <Line key={start + i} line={line} />
          ))}
        </Box>
      </Box>
    </div>
  )
}
