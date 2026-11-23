import { Fragment, useState } from 'react'
import { Paper } from '@mantine/core'
import CardConnector from './CardConnector'
import { firstOpenCard } from './firstOpenCard'

// The SettingsCards of one risk level, in order, in a bordered wrapper (a
// lone card stands bare). One is open at a time: opening a card closes the
// others, and the first card not yet configured opens at first. Each card is
// `{ key, configured, render }`, where `render` gets the `open`, `onToggle`
// and `done` props of its SettingsCard. The connector before a closed card
// that is not configured carries "Next".
export default function CardStack({ cards }) {
  const [openKey, setOpenKey] = useState(() => firstOpenCard(cards))
  const stack = cards.map((card, index) => {
    const open = openKey === card.key
    return (
      <Fragment key={card.key}>
        {index > 0 && <CardConnector next={!open && !card.configured} />}
        {card.render({
          open,
          done: card.configured,
          onToggle: () => setOpenKey((current) => (current === card.key ? null : card.key)),
        })}
      </Fragment>
    )
  })
  if (cards.length === 1) return stack
  return (
    <Paper withBorder radius="lg" p="md">
      {stack}
    </Paper>
  )
}
