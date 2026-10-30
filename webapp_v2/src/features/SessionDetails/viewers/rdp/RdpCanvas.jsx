import { useEffect, useRef } from 'react'
import { alpha, useMantineTheme } from '@mantine/core'
import { activeDetections, entityColor } from './rdpLogic'
import { drawBitmap } from './rleDecoder'
import classes from './Rdp.module.css'

// The recording background before its first bitmap.
const SCREEN_BLACK = '#000'

// A bitmap is a region update, so a frame is everything drawn up to it. Forward
// playback paints only the new frames; going back repaints from the first one.
export default function RdpCanvas({ frames, index, time, width, height, snapshots, enabledTypes, children }) {
  const canvasRef = useRef(null)
  const overlayRef = useRef(null)
  const drawnRef = useRef(-1)
  const theme = useMantineTheme()

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas || !frames.length) return
    const ctx = canvas.getContext('2d', { alpha: false })
    ctx.imageSmoothingEnabled = false
    const target = Math.min(index, frames.length - 1)
    let from = drawnRef.current + 1
    if (drawnRef.current < 0 || target < drawnRef.current) {
      ctx.fillStyle = SCREEN_BLACK
      ctx.fillRect(0, 0, width, height)
      from = 0
    }
    for (let i = from; i <= target; i += 1) {
      if (frames[i].bitmap) drawBitmap(ctx, frames[i].bitmap)
    }
    drawnRef.current = target
  }, [frames, index, width, height])

  useEffect(() => {
    const overlay = overlayRef.current
    if (!overlay) return
    const ctx = overlay.getContext('2d')
    ctx.clearRect(0, 0, width, height)
    for (const detection of activeDetections(snapshots, time) || []) {
      const { x, y, width: w, height: h, entity_type: type } = detection
      if (!(w > 0 && h > 0) || !enabledTypes.has(type)) continue
      const color = theme.colors[entityColor(type)]?.[6] || theme.colors.gray[6]
      ctx.fillStyle = alpha(color, 0.25)
      ctx.strokeStyle = alpha(color, 0.85)
      ctx.lineWidth = 1
      ctx.fillRect(x, y, w, h)
      ctx.strokeRect(x, y, w, h)
    }
  }, [snapshots, time, enabledTypes, width, height, theme])

  return (
    <div className={classes.stage}>
      <canvas ref={canvasRef} width={width} height={height} className={classes.canvas} role="img" aria-label="RDP session recording" />
      <canvas ref={overlayRef} width={width} height={height} className={classes.overlay} aria-hidden="true" />
      {children}
    </div>
  )
}
