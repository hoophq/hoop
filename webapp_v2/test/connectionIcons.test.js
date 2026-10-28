import { beforeEach, describe, expect, it } from 'vitest'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { getConnectionIcon } from '@/utils/connectionIcons'

beforeEach(() => {
  useConnectionsMetadataStore.setState({
    metadata: {
      connections: [
        { id: 'httpproxy', 'icon-name': 'httpproxy', resourceConfiguration: { type: 'httpproxy', subtype: 'web-application' } },
        { id: 'webapps-and-apis', 'icon-name': 'tcp', resourceConfiguration: { type: 'application', subtype: 'tcp' } },
      ],
    },
  })
})

describe('getConnectionIcon', () => {
  it('matches the catalog subtype first', () => {
    expect(getConnectionIcon({ type: 'application', subtype: 'tcp' })).toBe('/icons/connections/tcp-default.svg')
  })

  it('matches an HTTP proxy role by the catalog id', () => {
    expect(getConnectionIcon({ type: 'httpproxy', subtype: 'httpproxy' })).toBe('/icons/connections/httpproxy-default.svg')
  })

  it('falls back for a subtype the catalog does not know', () => {
    expect(getConnectionIcon({ type: 'custom', subtype: 'cloudwatch' })).toBe('/icons/connections/custom-ssh.svg')
  })
})
