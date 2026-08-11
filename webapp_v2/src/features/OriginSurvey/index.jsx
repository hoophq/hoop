import { useState } from 'react'
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

  // 'collapsed' → launcher only, 'open' → collecting the answer, 'closed' →
  // answered, gone for this page load. The local phase outranks the server
  // flag, which is only re-read on the next load.
  const [phase, setPhase] = useState('collapsed')
  const [origin, setOrigin] = useState(null)
  const [otherText, setOtherText] = useState('')

  function handleSubmit() {
    // Fire and forget: the widget closes on the click without waiting for the
    // gateway, so a slow or lost response can never hold the UI. If the write
    // does not land, /userinfo keeps reporting show_origin_survey on the next
    // load and the user is simply asked again — there is nothing for them to
    // act on in the meantime, so a failure toast would be pure noise. It still
    // reaches the console for anyone debugging the flow.
    originSurveyService
      .answer({ origin, originOther: otherText.trim() })
      .catch((err) => console.warn('[origin-survey] failed recording the answer:', err))

    setPhase('closed')
  }

  if (phase === 'closed') return null
  if (phase === 'collapsed' && !user?.show_origin_survey) return null

  const expanded = phase === 'open'
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
                onClick={() => setPhase('collapsed')}
                aria-label="Collapse survey"
              >
                <X size={16} />
              </ActionIcon>
            </Group>

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
          </Stack>
        </Paper>
      )}

      {/* The dot marks the survey as still pending, and stays on while the card
          is open — it only goes away with the whole widget, once answered. */}
      <Indicator color="red" size={8} offset={4} withBorder>
        <ActionIcon
          variant="default"
          radius="xl"
          size="lg"
          className={classes.launcher}
          onClick={() => setPhase(expanded ? 'collapsed' : 'open')}
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
