import { describe, expect, it } from 'vitest'
import { jsonCredentialToField } from '@/utils/connectionsMetadataMapper'

describe('jsonCredentialToField', () => {
  it('keys a filesystem credential as filesystem:<NAME>', () => {
    const field = jsonCredentialToField({ name: 'GOOGLE_APPLICATION_CREDENTIALS', type: 'filesystem', required: true })
    expect(field.envKey).toBe('filesystem:GOOGLE_APPLICATION_CREDENTIALS')
    expect(field.type).toBe('textarea')
  })

  it('leaves an env-var credential on the default envvar: key', () => {
    const field = jsonCredentialToField({ name: 'CLOUDSDK_CORE_PROJECT', type: 'env-var', required: true })
    expect(field.envKey).toBeUndefined()
    expect(field.key).toBe('cloudsdk_core_project')
  })
})
