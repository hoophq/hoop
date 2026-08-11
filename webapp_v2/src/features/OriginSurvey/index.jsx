import { useEffect, useState } from 'react'
import { Group, Paper, Stack, Text } from '@mantine/core'
import { X } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import Radio from '@/components/Radio'
import TextInput from '@/components/TextInput'
import { useUserStore } from '@/stores/useUserStore'
import { originSurveyService } from '@/services/originSurvey'
import { showSnackbar } from '@/utils/snackbar'
import {
  ORIGIN_OPTIONS,
  ORIGIN_OTHER,
  ORIGIN_OTHER_MAX_LENGTH,
  SNOOZE_STORAGE_KEY,
} from './constants'
import classes from './OriginSurvey.module.css'

// Both webapps boot Intercom with hide_default_launcher, so nothing else
// occupies the bottom-right corner. The offset matches the 1.5rem the global
// Toaster uses on the opposite corner.
const EDGE_OFFSET = 24

const CARD_WIDTH = 340

// How long the "Answer received" acknowledgement stays on screen before the
// card removes itself.
const ACK_DURATION_MS = 2500

/**
 * Onboarding "How did you hear about Hoop?" survey — a non-blocking card in the
 * bottom-right corner, mounted globally so it also reaches the chrome-less
 * onboarding routes that render outside the app Layout.
 *
 * Visibility is owned by the gateway: /userinfo returns show_origin_survey,
 * which is true only while the user has not answered and is within 7 days of
 * their user record being created. Dismissing with the X only snoozes the card
 * for the current tab session.
 */
function OriginSurvey() {
  const user = useUserStore((state) => state.user)

  // 'form' → collecting the answer, 'ack' → showing the acknowledgement,
  // 'closed' → done for this page load, whether answered or dismissed. The
  // local phase outranks the server flag so the acknowledgement survives on
  // screen after the answer is recorded.
  const [phase, setPhase] = useState(() =>
    sessionStorage.getItem(SNOOZE_STORAGE_KEY) === 'true' ? 'closed' : 'form',
  )
  const [origin, setOrigin] = useState(null)
  const [otherText, setOtherText] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (phase !== 'ack') return
    const timer = setTimeout(() => setPhase('closed'), ACK_DURATION_MS)
    return () => clearTimeout(timer)
  }, [phase])

  function handleDismiss() {
    sessionStorage.setItem(SNOOZE_STORAGE_KEY, 'true')
    setPhase('closed')
  }

  async function handleSubmit() {
    setSubmitting(true)
    try {
      await originSurveyService.answer({ origin, originOther: otherText.trim() })
    } catch (err) {
      // 409 means this user already answered from another tab or device. The
      // desired state is reached either way, so acknowledge instead of asking
      // again — any other failure keeps the form open so the answer is not lost.
      if (err.response?.status !== 409) {
        showSnackbar({
          level: 'error',
          text: 'Failed to send your answer',
          description: err.response?.data?.message,
        })
        setSubmitting(false)
        return
      }
    }
    setPhase('ack')
  }

  if (phase === 'closed') return null
  if (phase === 'form' && !user?.show_origin_survey) return null

  const otherSelected = origin === ORIGIN_OTHER
  const canSubmit = origin !== null && (!otherSelected || otherText.trim() !== '')

  return (
    <Paper
      shadow="md"
      radius="sm"
      p="md"
      w={CARD_WIDTH}
      pos="fixed"
      bottom={EDGE_OFFSET}
      right={EDGE_OFFSET}
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
            aria-label="Dismiss survey"
          >
            <X size={16} />
          </ActionIcon>
        </Group>

        {phase === 'ack' ? (
          <Text size="sm">Answer received</Text>
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
              <Button size="xs" disabled={!canSubmit} loading={submitting} onClick={handleSubmit}>
                Submit
              </Button>
            </Group>
          </>
        )}
      </Stack>
    </Paper>
  )
}

export default OriginSurvey
