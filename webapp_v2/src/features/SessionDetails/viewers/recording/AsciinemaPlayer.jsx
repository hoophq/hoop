import { useEffect, useRef } from 'react'
import * as asciinema from 'asciinema-player'
import 'asciinema-player/dist/bundle/asciinema-player.css'
import classes from './recording.module.css'

// Replays decoded terminal events ([seconds, type, text]) as an asciicast v2.
// Loaded with the Video view only.
export default function AsciinemaPlayer({ width, height, events }) {
  const mount = useRef(null)

  useEffect(() => {
    const header = { version: 2, title: 'Recording', width, height, env: { TERM: 'xterm-256color' } }
    const player = asciinema.create({ data: [header, ...events] }, mount.current, { loop: true, fit: 'both' })
    return () => player.dispose()
  }, [width, height, events])

  return <div ref={mount} className={classes.player} />
}
