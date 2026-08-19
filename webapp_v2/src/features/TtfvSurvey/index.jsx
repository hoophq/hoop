import { useState } from 'react'
import { Group, Text } from '@mantine/core'
import Button from '@/components/Button'
import SurveyWidget from '@/components/SurveyWidget'
import { EMPTY_CHOICE_ANSWER } from '@/components/SurveyWidget/constants'
import { useUserStore } from '@/stores/useUserStore'
import { ttfvSurveyService } from '@/services/ttfvSurvey'
import {
  ACTIVITY_OPTIONS,
  ACTIVITY_OTHER,
  ACTIVITY_OTHER_MAX_LENGTH,
  ACTIVITY_QUESTION,
  REACHED_VALUE_QUESTION,
} from './constants'

/**
 * Time-to-first-value survey. TTFV is the gap between an organization being
 * created and the first moment an admin confirms they got value out of hoop;
 * the product is self-hosted, so the only way to observe that moment is to ask.
 *
 * Two steps: a Yes/No, and — only after a Yes — what it was they got done. A No
 * is an answer too, recorded on the spot, and the gateway asks again once its
 * cooldown has passed.
 *
 * Visibility is owned entirely by the gateway: /userinfo returns
 * show_ttfv_survey, which already accounts for the caller being an admin,
 * whether the organization is eligible to be asked at all, the terminal Yes and
 * the cooldown. Nothing about that policy is re-implemented here — not even the
 * names of its clauses — so it can be re-tuned without a frontend deploy, and a
 * gateway that does not know the field yet leaves it undefined, which reads as
 * "do not ask".
 *
 * Rendered through `features/Surveys`, never mounted directly — see the note
 * there about two surveys sharing one anchor.
 */
function TtfvSurvey() {
  const due = useUserStore((state) => !!state.user?.show_ttfv_survey)
  const [showActivity, setShowActivity] = useState(false)
  const [answer, setAnswer] = useState(EMPTY_CHOICE_ANSWER)
  const [answered, setAnswered] = useState(false)

  // Fire and forget, like the origin survey: the acknowledgement shows on the
  // click without waiting for the gateway. If the write does not land,
  // /userinfo keeps reporting show_ttfv_survey and the admin is asked again —
  // there is nothing for them to act on in the meantime, so a failure toast
  // would be pure noise. It still reaches the console for anyone debugging.
  function record(payload) {
    ttfvSurveyService
      .answer(payload)
      .catch((err) => console.warn('[ttfv-survey] failed recording the answer:', err))

    setAnswered(true)
  }

  return (
    <SurveyWidget label={REACHED_VALUE_QUESTION} due={due} answered={answered}>
      {showActivity ? (
        <SurveyWidget.ChoiceQuestion
          question={ACTIVITY_QUESTION}
          options={ACTIVITY_OPTIONS}
          answer={answer}
          onAnswerChange={setAnswer}
          otherValue={ACTIVITY_OTHER}
          otherMaxLength={ACTIVITY_OTHER_MAX_LENGTH}
          otherLabel="Specify what you got done"
          onSubmit={() =>
            record({
              reachedValue: true,
              activity: answer.value,
              activityOther: answer.otherText.trim(),
            })
          }
        />
      ) : (
        <>
          <Text size="sm">{REACHED_VALUE_QUESTION}</Text>
          {/* Yes advances to the follow-up rather than answering: a bare
              "reached value" with no activity is not worth a row, and the
              gateway treats the first Yes as terminal. */}
          <Group justify="flex-end" gap="md">
            <Button size="xs" onClick={() => setShowActivity(true)}>
              Yes
            </Button>
            <Button size="xs" variant="default" onClick={() => record({ reachedValue: false })}>
              No
            </Button>
          </Group>
        </>
      )}
    </SurveyWidget>
  )
}

export default TtfvSurvey
