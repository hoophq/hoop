import { AnsiHtml } from 'fancy-ansi/react'
import classes from './recording.module.css'

// Terminal text with its ANSI colors.
export default function LogsText({ text }) {
  return (
    <div className={classes.terminal}>
      <AnsiHtml text={text} />
    </div>
  )
}
