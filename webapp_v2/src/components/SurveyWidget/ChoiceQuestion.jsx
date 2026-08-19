import { Group, Stack } from '@mantine/core'
import Button from '@/components/Button'
import Radio from '@/components/Radio'
import TextInput from '@/components/TextInput'

/**
 * A single-choice survey question with a free-text follow-up on one designated
 * option, plus the Submit button that closes it. Both surveys ask one of these,
 * and the interlock between the two inputs — Submit stays disabled until the
 * free text is filled in for the "other" option — is the part worth having in
 * one place.
 *
 * Controlled rather than self-contained: SurveyWidget unmounts the card when
 * the user collapses it with the X, so state kept in here would be wiped by a
 * mis-click. Holding it in the survey feature, which stays mounted, is what
 * makes collapsing free.
 *
 * Renders a fragment — the pieces are spaced by the card's own Stack.
 */
function ChoiceQuestion({
  question,
  options,
  answer,
  onAnswerChange,
  otherValue,
  otherMaxLength,
  otherLabel,
  onSubmit,
}) {
  const otherSelected = answer.value === otherValue
  const canSubmit = answer.value !== null && (!otherSelected || answer.otherText.trim() !== '')

  return (
    <>
      <Radio.Group
        value={answer.value}
        onChange={(value) => onAnswerChange({ ...answer, value })}
        label={question}
        labelProps={{ fw: 400 }}
      >
        <Stack gap="sm" mt="sm">
          {options.map((option) => (
            <Radio key={option.value} value={option.value} label={option.label} size="xs" />
          ))}
        </Stack>
      </Radio.Group>

      {otherSelected && (
        <TextInput
          placeholder="Specify"
          value={answer.otherText}
          onChange={(event) => onAnswerChange({ ...answer, otherText: event.currentTarget.value })}
          maxLength={otherMaxLength}
          aria-label={otherLabel}
        />
      )}

      <Group justify="flex-end">
        <Button size="xs" disabled={!canSubmit} onClick={onSubmit}>
          Submit
        </Button>
      </Group>
    </>
  )
}

export default ChoiceQuestion
