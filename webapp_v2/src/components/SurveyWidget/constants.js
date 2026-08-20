// Starting value for the `answer` a SurveyWidget.ChoiceQuestion is driven by.
// Never mutated — ChoiceQuestion only ever hands back a new object — so the two
// surveys can safely share the one literal.
export const EMPTY_CHOICE_ANSWER = { value: null, otherText: '' }
