import { expect, test } from 'vitest'
import { sidecarImage } from '@/pages/Sidecars/Setup/image'

test('a release version names its own image', () => {
  expect(sidecarImage('1.285.1')).toBe('hoophq/hoopsidecar:1.285.1')
})

test('a version with no published image falls back to latest', () => {
  for (const v of ['unknown', '', null, undefined, '2107.0.0-gabc1234', 'v1.285.1']) {
    expect(sidecarImage(v)).toBe('hoophq/hoopsidecar:latest')
  }
})
