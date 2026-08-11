import api from './api'

export const originSurveyService = {
  // POST /users/self/signup-origin → 204 No Content.
  // origin_other is only read by the API when origin is 'other'.
  // Answering twice returns 409 — the first answer wins.
  answer: ({ origin, originOther }) =>
    api.post('/users/self/signup-origin', { origin, origin_other: originOther ?? '' }),
}
