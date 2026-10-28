import { describe, expect, it } from 'vitest'
import { canConnect, canTestConnection } from '@/utils/connectionPolicy'

describe('canTestConnection', () => {
  it('allows only the shapes the gateway has a probe for', () => {
    for (const subtype of ['postgres', 'mysql', 'mssql', 'mongodb', 'oracledb']) {
      expect(canTestConnection({ type: 'database', subtype })).toBe(true)
    }
    expect(canTestConnection({ type: 'custom', subtype: 'dynamodb' })).toBe(true)
    expect(canTestConnection({ type: 'custom', subtype: 'cloudwatch' })).toBe(true)
  })

  it('hides it for SSH, TCP and other shapes the gateway answers 400 for', () => {
    expect(canTestConnection({ type: 'application', subtype: 'ssh' })).toBe(false)
    expect(canTestConnection({ type: 'application', subtype: 'tcp' })).toBe(false)
    expect(canTestConnection({ type: 'custom', subtype: 'aws-cloudwatch' })).toBe(false)
    expect(canTestConnection({ type: 'custom', subtype: 'bigquery' })).toBe(false)
  })
})

describe('canConnect', () => {
  it('is false only when every access mode is disabled', () => {
    const off = { access_mode_runbooks: 'disabled', access_mode_exec: 'disabled', access_mode_connect: 'disabled' }
    expect(canConnect(off)).toBe(false)
    expect(canConnect({ ...off, access_mode_exec: 'enabled' })).toBe(true)
  })
})
