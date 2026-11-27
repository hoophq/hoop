import { readFileSync } from 'node:fs'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const trackEvent = vi.fn()
const getSignupUrl = vi.fn()
vi.mock('@/stores/useUserStore', () => ({ useUserStore: { getState: () => ({ trackEvent }) } }))
vi.mock('@/services/auth', () => ({ authService: { getSignupUrl: (...args) => getSignupUrl(...args) } }))
vi.mock('@/utils/snackbar', () => ({ showSnackbar: vi.fn() }))

const replace = vi.fn()
vi.stubGlobal('window', { location: { origin: 'https://gateway.test', replace } })

const { sentryEnabled } = await import('@/services/sentry')
const { clarityCommand } = await import('@/services/clarity')
const { createClipboardBlocker } = await import('@/hooks/useClipboardBlock')
const { startIdpSignup } = await import('@/pages/Auth/idpSignup')
const { EVENTS } = await import('@/services/analytics')

const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8')

describe('Sentry', () => {
  it('starts only with a DSN, a sample rate and analytics tracking off, as CLJS', () => {
    expect(sentryEnabled({ dsn: 'https://k@sentry.test/1', sampleRate: '1.0', analyticsTracking: false })).toBe(true)
    expect(sentryEnabled({ dsn: 'https://k@sentry.test/1', sampleRate: '1.0', analyticsTracking: true })).toBe(false)
    expect(sentryEnabled({ dsn: '', sampleRate: '1.0', analyticsTracking: false })).toBe(false)
    expect(sentryEnabled({ dsn: 'https://k@sentry.test/1', sampleRate: '', analyticsTracking: false })).toBe(false)
  })

  it('uses the CLJS DSNs: dev for vite, release for vite build', () => {
    const dsns = (text) => [...text.matchAll(/https:\/\/[0-9a-f]+@[\w.]+sentry\.io\/\d+/g)].map((m) => m[0])
    const shadow = dsns(read('../../webapp/shadow-cljs.edn'))
    const vite = read('../vite.config.js')
    expect(shadow).toHaveLength(2)
    expect(vite).toContain(`serve: '${shadow[0]}'`)
    expect(vite).toContain(`build: '${shadow[1]}'`)
  })
})

describe('Clarity', () => {
  const run = { analyticsTracking: true, hostname: 'app.example.com', orgName: 'acme' }

  it('starts with analytics tracking on, on a customer host', () => {
    expect(clarityCommand(run)).toBe('start')
  })

  it('stops with analytics tracking off', () => {
    expect(clarityCommand({ ...run, analyticsTracking: false })).toBe('stop')
  })

  it.each(['localhost', '127.0.0.1', 'appdemo.hoop.dev', 'tryrunops.hoop.dev'])('stops on %s', (hostname) => {
    expect(clarityCommand({ ...run, hostname })).toBe('stop')
  })

  it('stops for the hoopdev org', () => {
    expect(clarityCommand({ ...run, orgName: 'hoopdev' })).toBe('stop')
  })
})

describe('clipboard block', () => {
  it('blocks every event and notifies at most once per 2 s', () => {
    let t = 10_000
    const notify = vi.fn()
    const block = createClipboardBlocker({ notify, now: () => t })
    const event = () => ({ preventDefault: vi.fn() })

    const first = event()
    block(first)
    t += 1_000
    const second = event()
    block(second)
    t += 1_500
    block(event())

    expect(first.preventDefault).toHaveBeenCalled()
    expect(second.preventDefault).toHaveBeenCalled()
    expect(notify).toHaveBeenCalledTimes(2)
  })
})

describe('signup start', () => {
  beforeEach(() => {
    trackEvent.mockClear()
    replace.mockClear()
  })

  it('sends the CLJS event name', () => {
    expect(read('../../webapp/src/webapp/app.cljs')).toContain(`"${EVENTS.SIGNUP_START}"`)
  })

  it('tracks the event, then opens the IdP signup', async () => {
    getSignupUrl.mockResolvedValue('https://idp.test/signup')
    await startIdpSignup()
    expect(trackEvent).toHaveBeenCalledWith('SignUp - start signup')
    expect(getSignupUrl).toHaveBeenCalledWith('https://gateway.test/signup/callback')
    expect(replace).toHaveBeenCalledWith('https://idp.test/signup')
  })

  it('does not navigate when the signup link fails', async () => {
    getSignupUrl.mockRejectedValue(new Error('down'))
    await expect(startIdpSignup()).rejects.toThrow('down')
    expect(replace).not.toHaveBeenCalled()
  })
})
