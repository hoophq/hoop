import { describe, expect, it } from 'vitest'
import { isWithinTimeWindow } from '@/utils/datetime'

const at = (hhmmss) => new Date(`2026-10-30T${hhmmss}Z`)

// The gateway's own cases are in gateway/api/session/run_exec_window_test.go.
describe('isWithinTimeWindow matches the gateway to the second', () => {
  it.each([
    ['09:00', '17:00', '09:00:00', true],
    ['09:00', '17:00', '17:00:00', true],
    ['09:00', '17:00', '17:00:01', false],
    ['09:00', '17:00', '08:59:59', false],
    ['22:00', '02:00', '23:30:00', true],
    ['22:00', '02:00', '01:59:59', true],
    ['22:00', '02:00', '02:00:00', true],
    ['22:00', '02:00', '02:00:30', false],
    ['22:00', '02:00', '21:59:59', false],
  ])('%s-%s at %s is %s', (start, end, now, expected) => {
    expect(isWithinTimeWindow(start, end, at(now))).toBe(expected)
  })
})
