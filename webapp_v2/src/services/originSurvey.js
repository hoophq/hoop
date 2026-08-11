import api from './api'

// The shared axios instance has no timeout on purpose — some endpoints
// (runbook exec, database explorer) legitimately run for minutes. The survey is
// the opposite: a small, optional write that must never leave the user staring
// at a spinner if a response goes missing. Bounding it here keeps that
// guarantee without touching the long-running endpoints.
//
// Retrying after a timeout is safe: if the first attempt did reach the gateway
// the second returns 409, which the caller treats as answered.
const REQUEST_TIMEOUT_MS = 15000

export const originSurveyService = {
  // POST /users/self/signup-origin → 204 No Content.
  // origin_other is only read by the API when origin is 'other'.
  // Answering twice returns 409 — the first answer wins.
  answer: ({ origin, originOther }) =>
    api.post(
      '/users/self/signup-origin',
      { origin, origin_other: originOther ?? '' },
      { timeout: REQUEST_TIMEOUT_MS },
    ),
}
