import { useState } from 'react'
import SurveyWidget from '@/components/SurveyWidget'
import { EMPTY_CHOICE_ANSWER } from '@/components/SurveyWidget/constants'
import { useUserStore } from '@/stores/useUserStore'
import { originSurveyService } from '@/services/originSurvey'
import { ORIGIN_OPTIONS, ORIGIN_OTHER, ORIGIN_OTHER_MAX_LENGTH, ORIGIN_QUESTION } from './constants'

/**
 * Onboarding "How did you hear about Hoop?" survey.
 *
 * Visibility is owned by the gateway: /userinfo returns show_origin_survey,
 * which is true only while the user has not answered and is within 7 days of
 * their user record being created.
 *
 * Rendered through `features/Surveys`, never mounted directly — see the note
 * there about two surveys sharing one anchor.
 */
function OriginSurvey() {
  const due = useUserStore((state) => !!state.user?.show_origin_survey)
  const [answer, setAnswer] = useState(EMPTY_CHOICE_ANSWER)
  const [answered, setAnswered] = useState(false)

  function handleSubmit() {
    // Fire and forget: the acknowledgement shows on the click without waiting
    // for the gateway, so a slow or lost response can never hold the UI. If the
    // write does not land, /userinfo keeps reporting show_origin_survey on the
    // next load and the user is simply asked again — there is nothing for them
    // to act on in the meantime, so a failure toast would be pure noise. It
    // still reaches the console for anyone debugging the flow.
    originSurveyService
      .answer({ origin: answer.value, originOther: answer.otherText.trim() })
      .catch((err) => console.warn('[origin-survey] failed recording the answer:', err))

    setAnswered(true)
  }

  return (
    <SurveyWidget label={ORIGIN_QUESTION} due={due} answered={answered}>
      <SurveyWidget.ChoiceQuestion
        question={ORIGIN_QUESTION}
        options={ORIGIN_OPTIONS}
        answer={answer}
        onAnswerChange={setAnswer}
        otherValue={ORIGIN_OTHER}
        otherMaxLength={ORIGIN_OTHER_MAX_LENGTH}
        otherLabel="Specify how you heard about Hoop"
        onSubmit={handleSubmit}
      />
    </SurveyWidget>
  )
}

export default OriginSurvey
