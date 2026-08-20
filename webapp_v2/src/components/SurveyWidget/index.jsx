import { useEffect, useState } from 'react'
import { Box, Group, Indicator, Paper, Stack, Text } from '@mantine/core'
import { X } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import ChoiceQuestion from './ChoiceQuestion'
import classes from './SurveyWidget.module.css'

const CARD_WIDTH = 340

// Large enough to read as a call to action on the launcher rather than as a
// decorative dot.
const PENDING_DOT_SIZE = 12

// How long the acknowledgement stays on screen before the widget removes
// itself. Long enough to read a two-word message without lingering.
const ACK_DURATION_MS = 3000

// Card chrome rather than survey content: every survey wears the same header
// and the same acknowledgement, so the widget stays recognisable whichever one
// happens to be due. Only the question inside changes.
const CARD_TITLE = 'Help us out'
const ACK_MESSAGE = 'Answer received'

/**
 * The shell every in-app survey is rendered in: a floating launcher on the
 * right edge of the viewport that expands into a non-blocking card, plus the
 * acknowledgement that replaces the question once it is answered.
 *
 * It owns the presentation and the open/collapsed/acknowledged lifecycle. It
 * does not own visibility or submission — the survey feature passes `due` (the
 * gateway's verdict on whether to ask at all) and flips `answered` once it has
 * fired the write.
 *
 * Only one survey may be on screen at a time, since they all anchor to the same
 * spot; `features/Surveys` is what enforces that.
 *
 * @param {string} label   Describes the survey to assistive tech. Used on both
 *                         the launcher and the card, so it must stay stable
 *                         across a multi-step survey rather than tracking the
 *                         current step.
 * @param {boolean} due    Whether the gateway still wants this survey asked.
 * @param {boolean} answered Set by the feature once the answer is on its way.
 */
function SurveyWidget({ label, due, answered, children }) {
  // 'collapsed' → launcher only, 'open' → showing the question, 'closed' →
  // gone for this page load. The acknowledgement is not a phase of its own: it
  // is driven by `answered`, which the feature owns.
  const [phase, setPhase] = useState('collapsed')

  useEffect(() => {
    if (!answered) return
    const timer = setTimeout(() => setPhase('closed'), ACK_DURATION_MS)
    return () => clearTimeout(timer)
  }, [answered])

  // Once answered there is nothing left to come back to, so the X retires the
  // widget instead of collapsing it — it only reappears if the gateway still
  // reports the survey on the next load.
  function handleDismiss() {
    setPhase(answered ? 'closed' : 'collapsed')
  }

  function handleToggle() {
    // Inert while acknowledging: the answer is already on its way, reopening
    // the form would only invite a second submission.
    if (answered) return
    setPhase(phase === 'open' ? 'collapsed' : 'open')
  }

  if (phase === 'closed') return null
  // Only checked while collapsed: a card the user already opened stays open
  // even if the flag were to change underneath it.
  if (phase === 'collapsed' && !due) return null

  const expanded = phase === 'open' || answered

  return (
    <Box className={classes.widget} data-open={expanded || undefined}>
      {expanded && (
        <Paper
          shadow="md"
          radius="md"
          p="md"
          w={CARD_WIDTH}
          className={classes.card}
          role="dialog"
          aria-label={label}
        >
          <Stack gap="lg">
            <Group justify="space-between" align="flex-start" wrap="nowrap">
              <Text fw={500}>{CARD_TITLE}</Text>
              <ActionIcon
                variant="subtle"
                color="gray"
                onClick={handleDismiss}
                aria-label={answered ? 'Dismiss survey' : 'Collapse survey'}
              >
                <X size={16} />
              </ActionIcon>
            </Group>

            {answered ? (
              // The message replaces the question asynchronously, so it is
              // announced rather than silently swapped in.
              <Text size="sm" role="status">
                {ACK_MESSAGE}
              </Text>
            ) : (
              children
            )}
          </Stack>
        </Paper>
      )}

      {/* The dot marks the survey as still pending, so it goes out as soon as
          the answer is in — the launcher itself lingers only for the
          acknowledgement. */}
      <Indicator color="red" size={PENDING_DOT_SIZE} offset={4} withBorder disabled={answered}>
        <ActionIcon
          variant="default"
          radius="xl"
          size="lg"
          className={classes.launcher}
          onClick={handleToggle}
          aria-expanded={expanded}
          aria-label={label}
        >
          {/* The symbol SVG carries a viewBox but no width/height, so both axes
              are given here — with only a height it has no layout width to fall
              back on if the asset fails to load. viewBox is square. */}
          <img src="/images/hoop-branding/SVG/hoop-symbol_black.svg" alt="" width={18} height={18} />
        </ActionIcon>
      </Indicator>
    </Box>
  )
}

SurveyWidget.ChoiceQuestion = ChoiceQuestion

export default SurveyWidget
