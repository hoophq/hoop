import { Stack } from '@mantine/core'
import { CredentialField } from '../components/CredentialField'
import { getHostname, buildMcpUrl } from '../helpers'

// The gateway packs the three ready-to-run invocations into `command` as a JSON
// blob: { curl, browser, subdomain }.
function parseCommands(command) {
  if (!command) return {}
  try {
    return JSON.parse(command)
  } catch {
    return {}
  }
}

export function HttpProxyCredentials({ credentials }) {
  const { curl, browser, subdomain } = parseCommands(credentials.command)
  return (
    <Stack gap="md">
      <CredentialField label="Host" value={getHostname()} />
      <CredentialField label="Authorization Header" value={credentials.proxy_token} />
      <CredentialField label="Command cURL" value={curl} />
      <CredentialField label="Command Browser" value={browser} />
      <CredentialField label="Command Subdomain Browser" value={subdomain} />
      <CredentialField label="Port" value={credentials.port} />
    </Stack>
  )
}

export function McpCredentials({ credentials }) {
  return (
    <Stack gap="md">
      <CredentialField label="Host" value={getHostname()} />
      <CredentialField label="Port" value={credentials.port} />
      <CredentialField label="MCP URL" value={buildMcpUrl(credentials)} />
      <CredentialField label="Authorization Header" value={credentials.proxy_token} />
    </Stack>
  )
}
