import { describe, expect, it } from 'vitest'
import { formToListener, listenerToForm, validateListener } from '@/pages/Sidecars/listeners'

const lane = (extra) =>
  listenerToForm({ name: 'appdb', protocol: 'postgres', listen: '127.0.0.1:15432', upstream: 'db:5432', ...extra })

describe('max_conns', () => {
  it('saves -1, which is unlimited', () => {
    expect(validateListener(lane({ max_conns: -1 })).max_conns).toBeUndefined()
    expect(formToListener(lane({ max_conns: -1 })).max_conns).toBe(-1)
  })

  it('drops 0, which the sidecar reads as the default of 200', () => {
    expect(formToListener(lane({ max_conns: 0 })).max_conns).toBeUndefined()
  })

  it('refuses a value below -1', () => {
    expect(validateListener(lane({ max_conns: -2 })).max_conns).toBe('Cannot be below -1.')
  })

  it('still refuses a negative value on another integer field', () => {
    expect(validateListener(lane({ idle_timeout_sec: -1 })).idle_timeout_sec).toBe('Cannot be negative.')
  })
})
