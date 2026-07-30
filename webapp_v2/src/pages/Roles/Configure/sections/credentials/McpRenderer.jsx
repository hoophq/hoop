import { useEffect, useRef, useState } from 'react'
import { Group, Paper, Stack, Text, Title } from '@mantine/core'
import { Check, ShieldCheck } from 'lucide-react'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import PredefinedFields from '@/pages/Roles/Configure/sections/credentials/shared/PredefinedFields'
import HttpHeaders from '@/pages/Roles/Configure/sections/credentials/shared/HttpHeaders'
import AllowInsecureSsl from '@/pages/Roles/Configure/sections/credentials/shared/AllowInsecureSsl'
import AgentSelector from '@/pages/Roles/Configure/sections/credentials/shared/AgentSelector'
import {
  decodeSecretValue,
  encodeSecretValue,
} from '@/pages/Roles/Configure/utils/secretsCodec'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'
import { connectionsService } from '@/services/connections'
import { showSnackbar } from '@/utils/snackbar'

// MCP httpproxy connection editor. Mirrors CLJS
// webapp/.../resources/configure_role/mcp_edit.cljs: an MCP server URL,
// an "Authorize with MCP" OAuth widget that freezes the obtained access
// token into envvar:HEADER_AUTHORIZATION, plus the shared headers /
// insecure-SSL / agent sections. The Authorization token is managed by
// the widget, never shown as a plain header row (HEADERS_EXCLUDE).
//
// OAuth flow (gateway is the OAuth client — see
// gateway/api/connections/connection_mcp_oauth.go):
//   POST /mcp-oauth/authorize?redirect=<app-url> → { authorization_url, flow_id }
//   popup drives the upstream login; the gateway callback redirects the
//   popup back to <app-url>?mcp_oauth=success|error&flow_id=...
//   GET /mcp-oauth/token/{flow_id} (single-use) → { authorization_header }
// The header value is then staged like any other secret edit — nothing
// persists until the user saves.
const MCP_FIELDS = [
  {
    key: 'remote_url',
    label: 'MCP Server URL',
    required: true,
    placeholder: 'e.g. https://mcp.linear.app',
  },
]

const AUTH_HEADER_KEY = 'envvar:HEADER_AUTHORIZATION'
const HEADERS_EXCLUDE = [AUTH_HEADER_KEY]

const POPUP_TIMEOUT_MS = 5 * 60 * 1000
const POPUP_POLL_MS = 500
const POPUP_FEATURES = 'width=600,height=800,menubar=no,toolbar=no,location=yes'

// Reads the same-origin callback return from the popup. Returns the
// outcome ({ ok, flowId, reason }) once the popup navigated back to our
// origin with ?mcp_oauth=..., or null while still on the provider's
// (cross-origin) login page — reading .href throws there; expected.
function readPopupOutcome(popup) {
  let href
  try {
    href = popup.location.href
  } catch {
    return null
  }
  if (!href || !href.startsWith(window.location.origin)) return null
  const params = new URLSearchParams(popup.location.search)
  const outcome = params.get('mcp_oauth')
  if (!outcome) return null
  return {
    ok: outcome === 'success',
    flowId: params.get('flow_id'),
    reason: params.get('reason'),
  }
}

export default function McpRenderer({
  connection,
  availableSources,
  forceNewState,
  hideRoleInfo,
}) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)

  // Auth-flow-only inputs — never persisted as connection env vars.
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [authError, setAuthError] = useState(null)
  const pollTimer = useRef(null)

  useEffect(() => () => clearInterval(pollTimer.current), [])

  // Key presence is the existence signal (survives hide_role_info
  // masking). A staged delete un-authorizes; a staged replace/new
  // authorizes even before save.
  const staged = stagedSecrets[AUTH_HEADER_KEY]
  const persisted = Boolean(connection.secret && AUTH_HEADER_KEY in connection.secret)
  const authorized = staged
    ? staged.action !== 'delete' && Boolean(staged.value)
    : persisted

  const currentServerUrl = () => {
    const stagedUrl = stagedSecrets['envvar:REMOTE_URL']
    if (stagedUrl?.value) return decodeSecretValue(stagedUrl.value).trim()
    return decodeSecretValue(connection.secret?.['envvar:REMOTE_URL']).trim()
  }

  const finishError = (message, details) => {
    setBusy(false)
    setAuthError(message)
    showSnackbar({ level: 'error', text: 'MCP authorization failed', description: message, details })
  }

  const redeemToken = async (flowId) => {
    if (!flowId) {
      finishError('Missing flow id')
      return
    }
    try {
      const data = await connectionsService.mcpOAuthToken(flowId)
      if (!data?.authorization_header) {
        finishError('No token returned')
        return
      }
      replaceSecret(AUTH_HEADER_KEY, encodeSecretValue(data.authorization_header))
      setBusy(false)
      setAuthError(null)
      showSnackbar({ level: 'success', text: 'MCP connection authorized' })
    } catch (err) {
      finishError(err?.response?.data?.message || 'Failed to redeem token')
    }
  }

  const watchPopup = (popup) => {
    const started = Date.now()
    pollTimer.current = setInterval(() => {
      const stop = () => {
        clearInterval(pollTimer.current)
        pollTimer.current = null
      }
      if (popup.closed) {
        stop()
        setBusy(false)
        return
      }
      if (Date.now() - started > POPUP_TIMEOUT_MS) {
        stop()
        popup.close()
        finishError('Authorization timed out')
        return
      }
      const outcome = readPopupOutcome(popup)
      if (!outcome) return
      stop()
      popup.close()
      if (outcome.ok) {
        redeemToken(outcome.flowId)
      } else {
        const reason = outcome.reason
          ? `: ${outcome.reason.replace(/_/g, ' ')}`
          : ''
        finishError(`Authorization denied${reason}`)
      }
    }, POPUP_POLL_MS)
  }

  const authorize = async () => {
    const serverUrl = currentServerUrl()
    if (!serverUrl) {
      showSnackbar({
        level: 'error',
        text: 'Enter the MCP server URL before authorizing',
      })
      return
    }
    setBusy(true)
    setAuthError(null)
    // Strip query/hash so the popup lands on a clean app URL the gateway
    // callback can redirect back to.
    const redirect = window.location.origin + window.location.pathname
    const payload = { server_url: serverUrl }
    if (clientId.trim()) payload.client_id = clientId.trim()
    if (clientSecret.trim()) payload.client_secret = clientSecret.trim()
    try {
      const data = await connectionsService.mcpOAuthAuthorize(payload, redirect)
      if (!data?.authorization_url) {
        finishError('Authorization URL was not returned')
        return
      }
      const popup = window.open(data.authorization_url, 'hoop-mcp-oauth', POPUP_FEATURES)
      if (!popup) {
        finishError('Popup blocked. Allow popups for this site and retry.')
        return
      }
      watchPopup(popup)
    } catch (err) {
      finishError(err?.response?.data?.message || 'Failed to start authorization')
    }
  }

  const clearAuthorization = () => {
    if (persisted) deleteSecret(AUTH_HEADER_KEY)
    else cancelSecretChange(AUTH_HEADER_KEY)
    setAuthError(null)
  }

  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={4}>Basic info</Title>
        <PredefinedFields
          connection={connection}
          fields={MCP_FIELDS}
          availableSources={availableSources}
          forceNewState={forceNewState}
          hideRoleInfo={hideRoleInfo}
        />
      </Stack>

      <Paper withBorder radius="md" p="md">
        <Stack gap="md">
          <Stack gap={4}>
            <Title order={5}>MCP Authorization</Title>
            <Text size="sm" c="dimmed">
              {"Log in to the MCP server to obtain an access token. The token is stored in this connection's Authorization header."}
            </Text>
          </Stack>

          <Stack gap="sm">
            <TextInput
              label="Client ID (optional)"
              placeholder="Leave blank to register automatically"
              value={clientId}
              onChange={(e) => setClientId(e.currentTarget.value)}
            />
            <PasswordInput
              label="Client Secret (optional)"
              placeholder="Only if your client requires one"
              value={clientSecret}
              onChange={(e) => setClientSecret(e.currentTarget.value)}
            />
          </Stack>

          {authorized ? (
            <Group justify="space-between" align="center" gap="sm">
              <Group gap={6} align="center">
                <Check size={16} color="var(--mantine-color-green-8)" />
                <Text size="sm" fw={500} c="green.8">
                  {'Authorized — access token stored'}
                </Text>
              </Group>
              <Group gap="sm">
                <Button variant="light" disabled={busy} onClick={authorize}>
                  Re-authorize
                </Button>
                <Button variant="subtle" color="red" onClick={clearAuthorization}>
                  Clear
                </Button>
              </Group>
            </Group>
          ) : (
            <Stack gap="xs">
              <Button
                w="fit-content"
                leftSection={<ShieldCheck size={16} />}
                loading={busy}
                onClick={authorize}
              >
                {busy ? 'Authorizing…' : 'Authorize with MCP'}
              </Button>
              {authError && (
                <Text size="sm" c="red">
                  {authError}
                </Text>
              )}
            </Stack>
          )}
        </Stack>
      </Paper>

      <HttpHeaders
        connection={connection}
        availableSources={availableSources}
        excludeKeys={HEADERS_EXCLUDE}
        hideRoleInfo={hideRoleInfo}
      />
      <AllowInsecureSsl connection={connection} />
      <AgentSelector />
    </Stack>
  )
}
