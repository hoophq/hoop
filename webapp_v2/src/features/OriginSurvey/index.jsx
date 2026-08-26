import { useEffect, useState } from 'react'
import { Box, Group, Indicator, Paper, Stack, Text } from '@mantine/core'
import { X } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import Radio from '@/components/Radio'
import TextInput from '@/components/TextInput'
import { useUserStore } from '@/stores/useUserStore'
import { originSurveyService } from '@/services/originSurvey'
import { ORIGIN_OPTIONS, ORIGIN_OTHER, ORIGIN_OTHER_MAX_LENGTH } from './constants'
import classes from './OriginSurvey.module.css'

const CARD_WIDTH = 340

// Large enough to read as a call to action on the launcher rather than as a
// decorative dot.
const PENDING_DOT_SIZE = 12

// How long the acknowledgement stays on screen before the widget removes
// itself. Long enough to read a two-word message without lingering.
const ACK_DURATION_MS = 3000

/**
 * Onboarding "How did you hear about Hoop?" survey — a floating launcher on the
 * right edge of the viewport that expands into a non-blocking card. Mounted
 * globally so it also reaches the chrome-less onboarding routes that render
 * outside the app Layout.
 *
 * Visibility is owned by the gateway: /userinfo returns show_origin_survey,
 * which is true only while the user has not answered and is within 7 days of
 * their user record being created. The X collapses the card back to the
 * launcher, so a mis-click never costs us the answer.
 */
function OriginSurvey() {
  const user = useUserStore((state) => state.user)

  // 'collapsed' → launcher only, 'open' → collecting the answer, 'ack' →
  // acknowledging it, 'closed' → gone for this page load. The local phase
  // outranks the server flag, which is only re-read on the next load.
  const [phase, setPhase] = useState('collapsed')
  const [origin, setOrigin] = useState(null)
  const [otherText, setOtherText] = useState('')

  useEffect(() => {
    if (phase !== 'ack') return
    const timer = setTimeout(() => setPhase('closed'), ACK_DURATION_MS)
    return () => clearTimeout(timer)
  }, [phase])

  function handleSubmit() {
    // Fire and forget: the acknowledgement shows on the click without waiting
    // for the gateway, so a slow or lost response can never hold the UI. If the
    // write does not land, /userinfo keeps reporting show_origin_survey on the
    // next load and the user is simply asked again — there is nothing for them
    // to act on in the meantime, so a failure toast would be pure noise. It
    // still reaches the console for anyone debugging the flow.
    originSurveyService
      .answer({ origin, originOther: otherText.trim() })
      .catch((err) => console.warn('[origin-survey] failed recording the answer:', err))

    setPhase('ack')
  }

  // Once answered there is nothing left to come back to, so the X retires the
  // widget instead of collapsing it — it only reappears if the gateway still
  // reports the survey on the next load.
  function handleDismiss() {
    setPhase(phase === 'ack' ? 'closed' : 'collapsed')
  }

  function handleToggle() {
    // Inert while acknowledging: the answer is already on its way, reopening
    // the form would only invite a second submission.
    if (phase === 'ack') return
    setPhase(phase === 'open' ? 'collapsed' : 'open')
  }

  if (phase === 'closed') return null
  if (phase === 'collapsed' && !user?.show_origin_survey) return null

  const answered = phase === 'ack'
  const expanded = phase === 'open' || answered
  const otherSelected = origin === ORIGIN_OTHER
  const canSubmit = origin !== null && (!otherSelected || otherText.trim() !== '')

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
          aria-label="How did you hear about Hoop?"
        >
          <Stack gap="lg">
            <Group justify="space-between" align="flex-start" wrap="nowrap">
              <Text fw={500}>Help us out</Text>
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
              // The message replaces the form asynchronously, so it is
              // announced rather than silently swapped in.
              <Text size="sm" role="status">
                Answer received
              </Text>
            ) : (
              <>
                <Radio.Group
                  value={origin}
                  onChange={setOrigin}
                  label="How did you hear about Hoop?"
                  labelProps={{ fw: 400 }}
                >
                  <Stack gap="sm" mt="sm">
                    {ORIGIN_OPTIONS.map((option) => (
                      <Radio key={option.value} value={option.value} label={option.label} size="xs" />
                    ))}
                  </Stack>
                </Radio.Group>

                {otherSelected && (
                  <TextInput
                    placeholder="Specify"
                    value={otherText}
                    onChange={(event) => setOtherText(event.currentTarget.value)}
                    maxLength={ORIGIN_OTHER_MAX_LENGTH}
                    aria-label="Specify how you heard about Hoop"
                  />
                )}

                <Group justify="flex-end">
                  <Button size="xs" disabled={!canSubmit} onClick={handleSubmit}>
                    Submit
                  </Button>
                </Group>
              </>
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
          aria-label="How did you hear about Hoop?"
        >
          {/* The symbol SVG carries a viewBox but no width/height, so both axes
              are given here — with only a height it has no layout width to fall
              back on if the asset fails to load. viewBox is square. */}
          <img
            src="/images/hoop-branding/SVG/hoop-symbol_black.svg"
            alt=""
            width={18}
            height={18}
          />
        </ActionIcon>
      </Indicator>
    </Box>
  )
}

export default OriginSurvey
