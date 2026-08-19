import api from './api'

export const ttfvSurveyService = {
  // POST /orgs/ttfv-survey → 204 No Content. Admin-only.
  // activity is only read by the API when reached_value is true, and
  // activity_other only when activity is 'other'.
  // Answering again after a "Yes" returns 409 — the first Yes stamps TTFV and
  // is terminal. A "No" is recorded and the survey comes back after a cooldown.
  answer: ({ reachedValue, activity, activityOther }) =>
    api.post('/orgs/ttfv-survey', {
      reached_value: reachedValue,
      activity: activity ?? '',
      activity_other: activityOther ?? '',
    }),
}
