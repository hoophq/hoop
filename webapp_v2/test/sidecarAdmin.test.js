import { describe, expect, it } from 'vitest'
import { ADMIN_ADDRESSES, adminWith } from '@/pages/Sidecars/config'

describe('the admin settings rows', () => {
  // admin.api_listen serves unauthenticated reads: an "Off" on the Admin row
  // must not hide it.
  it('show admin.api_listen on its own row', () => {
    expect(ADMIN_ADDRESSES.map((a) => a.key)).toEqual(['listen', 'api_listen'])
  })

  it('turn admin.listen off without dropping the other admin keys', () => {
    const admin = { listen: '0.0.0.0:19000', api_listen: '127.0.0.1:19002', ica_readiness_json: true }
    expect(adminWith(admin, 'listen', '  ')).toEqual({ api_listen: '127.0.0.1:19002', ica_readiness_json: true })
  })

  it('turn admin.api_listen off and keep admin.listen', () => {
    const admin = { listen: '0.0.0.0:19000', api_listen: '127.0.0.1:19002' }
    expect(adminWith(admin, 'api_listen', '')).toEqual({ listen: '0.0.0.0:19000' })
  })

  it('set a trimmed address on a sidecar with no admin block', () => {
    expect(adminWith(undefined, 'api_listen', ' 127.0.0.1:19002 ')).toEqual({ api_listen: '127.0.0.1:19002' })
  })
})
