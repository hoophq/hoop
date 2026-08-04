import { useEffect, useMemo, useRef, useState } from 'react'
import { Group, Paper, Stack, Text, Title } from '@mantine/core'
import { Check, ShieldCheck } from 'lucide-react'
import Button from '@/components/Button'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import NumberInput from '@/components/NumberInput'
import PredefinedFields from '@/pages/Roles/Configure/sections/credentials/shared/PredefinedFields'
import HttpHeaders from '@/pages/Roles/Configure/sections/credentials/shared/HttpHeaders'
import AllowInsecureSsl from '@/pages/Roles/Configure/sections/credentials/shared/AllowInsecureSsl'
import AgentSelector from '@/pages/Roles/Configure/sections/credentials/shared/AgentSelector'
import ToggleSection from '@/pages/Roles/Configure/components/ToggleSection'
import {
  PLACEHOLDER_KEY_RE,
  encodeSecretValue,
} from '@/pages/Roles/Configure/utils/secretsCodec'
import {
  AUTHORIZATION_KEY,
  DEFAULT_STATIC_HEADER,
  DEFAULT_TRANSPORT,
  ENV,
  HEADER_PREFIX,
  MCPENV_PREFIX,
  RUG_PULL_MODES,
  TRANSPORTS,
  authEnvValue,
  authModesFor,
  boolSetting,
  credentialHeaderKey,
  deriveAuthMode,
  headerKeyFor,
  headerKeys,
  headerNameOf,
  isStdio,
  matchCatalogEntry,
  staticHeaderFor,
  textSetting,
} from '@/pages/Roles/Configure/utils/mcpGateway'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'
import { connectionsService } from '@/services/connections'
import { showSnackbar } from '@/utils/snackbar'

// MCP Gateway (mcpproxy) connection editor — the protocol-aware MCP type
// (ADR-0004), as opposed to the legacy `mcp` subtype that relays MCP as opaque
// HTTP (McpRenderer.jsx).
//
// This form exists because the generic httpproxy renderer is wrong for this
// subtype in a way that is invisible until you look for it. That form shows a
// Remote URL, a headers list and an SSL toggle — so every setting that makes
// this connection type what it is (the backend transport, the tool policy, the
// per-session budgets, the child-process environment of a stdio server) is not
// merely unedited but unseeable, while REMOTE_URL is marked required and blocks
// Save on a stdio connection that legitimately has none.
//
// Field parity is with the CLJS create wizard (webapp/src/webapp/resources/
// setup/roles_step.cljs::mcpproxy-role-form), which still owns creation. The
// hard part of the edit direction is that three pieces of that form's state
// are deliberately never saved — see utils/mcpGateway.js, which derives them
// back from what the connection does carry.

const URL_FIELD = [
  {
    key: 'remote_url',
    label: 'MCP Server URL',
    required: true,
    placeholder: 'e.g. https://mcp.linear.app/mcp',
  },
]

const POPUP_TIMEOUT_MS = 5 * 60 * 1000
const POPUP_POLL_MS = 500
const POPUP_FEATURES = 'width=600,height=800,menubar=no,toolbar=no,location=yes'

// Reads the same-origin callback return from the popup. Null while the popup
// is still on the provider's login page — reading .href throws cross-origin,
// which is expected, not an error.
function readPopupOutcome(popup) {
  let search
  try {
    search = popup.location.search
  } catch {
    return null
  }
  if (!search) return null
  const params = new URLSearchParams(search)
  const outcome = params.get('mcp_oauth')
  if (!outcome) return null
  return {
    ok: outcome === 'success',
    flowId: params.get('flow_id'),
    reason: params.get('reason'),
  }
}

export default function McpProxyRenderer({
  connection,
  availableSources,
  forceNewState,
  connectionMethod,
  hideRoleInfo,
}) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const setMcpOAuthFlowId = useConfigureRoleStore((s) => s.setMcpOAuthFlowId)
  const pendingFlowId = useConfigureRoleStore((s) => s.mcpOAuthFlowId)
  const commandDraft = useConfigureRoleStore((s) => s.drafts.command)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)
  const commandLine = (commandDraft || []).join(' ')

  const [catalog, setCatalog] = useState({ status: 'idle', entries: [] })
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [authError, setAuthError] = useState(null)
  const pollTimer = useRef(null)

  useEffect(() => () => clearInterval(pollTimer.current), [])

  useEffect(() => {
    let cancelled = false
    setCatalog({ status: 'loading', entries: [] })
    connectionsService
      .mcpCatalog()
      .then((entries) => {
        if (!cancelled) setCatalog({ status: 'ready', entries: entries || [] })
      })
      .catch(() => {
        // A missing catalog costs the picker's descriptions and the per-server
        // auth-mode narrowing, not the ability to edit: every field below
        // still renders from the connection's own values.
        if (!cancelled) setCatalog({ status: 'error', entries: [] })
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Staged-first reads: what the user typed this session wins over what the
  // server returned, so the form reflects its own edits before any save.
  const secret = connection.secret || {}
  const readSetting = (key) => {
    const staged = stagedSecrets[key]
    if (staged) {
      return staged.action === 'delete' ? '' : textSetting({ [key]: staged.value }, key)
    }
    return textSetting(secret, key)
  }
  const readBool = (key, whenUnset) => {
    const staged = stagedSecrets[key]
    if (staged && staged.action !== 'delete') {
      return boolSetting({ [key]: staged.value }, key, whenUnset)
    }
    return boolSetting(secret, key, whenUnset)
  }

  const setSetting = (key, value) => replaceSecret(key, encodeSecretValue(value))

  const transport = readSetting(ENV.transport).trim() || DEFAULT_TRANSPORT
  const stdio = isStdio(transport)
  const remoteUrl = readSetting(ENV.remoteUrl)

  const entry = useMemo(
    () => matchCatalogEntry(catalog.entries, remoteUrl),
    [catalog.entries, remoteUrl],
  )

  // A staged HEADER_* write means the user is mid-edit on the credential; use
  // its key so the field they are typing into does not jump. Placeholder rows
  // are not credentials: the headers editor keeps one blank row staged at all
  // times, and adopting it renamed the token field "NEW_HEADER_1 value".
  const stagedCredentialKey = Object.keys(stagedSecrets).find(
    (k) =>
      k.startsWith(HEADER_PREFIX) &&
      !PLACEHOLDER_KEY_RE.test(k) &&
      stagedSecrets[k].action !== 'delete',
  )
  const persistedCredentialKey = credentialHeaderKey(secret)
  const catalogHeaderKey = entry ? headerKeyFor(staticHeaderFor(entry)) : null
  const credentialKey =
    stagedCredentialKey ||
    persistedCredentialKey ||
    catalogHeaderKey ||
    AUTHORIZATION_KEY
  const credentialHeaderName = headerNameOf(credentialKey)

  const granted = Boolean(connection.mcp_oauth_granted)
  const storedAuthMode = deriveAuthMode({ secret, granted, entry })
  const [authModeOverride, setAuthModeOverride] = useState(null)
  const authOptions = authModesFor(entry)
  const authMode =
    authModeOverride && authOptions.some((o) => o.value === authModeOverride)
      ? authModeOverride
      : storedAuthMode

  // A completed login this session, or a grant the gateway already holds.
  // Both mean the credential renews itself; a pasted token never does.
  const credentialStaged = stagedSecrets[credentialKey]
  const authorized = pendingFlowId
    ? true
    : credentialStaged
      ? credentialStaged.action !== 'delete' && Boolean(credentialStaged.value)
      : granted || Boolean(persistedCredentialKey)

  // ---- transport change -----------------------------------------------
  //
  // The transport decides what every other field MEANS, so switching sides of
  // the stdio/remote split has to drop the settings the previous side owned.
  // Left behind, an MCP_AUTH=passthrough chosen while remote survives into
  // stdio — which the agent rejects outright (validateMCPProxyEnv: a
  // subprocess has no inbound request to take a credential off) — and the
  // control that would fix it is hidden the moment stdio is selected.
  //
  // Moving between the two stdio transports changes only WHICH machine runs
  // the command, so nothing is dropped.
  const forget = (key) => {
    if (key in secret) deleteSecret(key)
    else cancelSecretChange(key)
  }

  const changeTransport = (next) => {
    if (!next || next === transport) return
    const wasStdio = stdio
    const willStdio = isStdio(next)
    setSetting(ENV.transport, next)
    if (wasStdio === willStdio) return

    setAuthModeOverride(null)
    setMcpOAuthFlowId(null)
    if (willStdio) {
      forget(ENV.remoteUrl)
      forget(ENV.auth)
      headerKeys(secret).forEach(forget)
      Object.keys(stagedSecrets)
        .filter((k) => k.startsWith(HEADER_PREFIX))
        .forEach(forget)
    } else {
      forget(`envvar:COMMAND`)
      Object.keys(secret)
        .filter((k) => k.startsWith(MCPENV_PREFIX))
        .forEach(forget)
      Object.keys(stagedSecrets)
        .filter((k) => k.startsWith(MCPENV_PREFIX))
        .forEach(forget)
    }
  }

  // ---- auth mode change ------------------------------------------------
  //
  // Switching mode forgets the credential the previous mode collected: a
  // leftover token would silently authenticate under a mode the admin
  // believes they replaced, and it cannot be narrowed by key — a frozen OAuth
  // token and a bearer PAT both live in the Authorization header.
  //
  // Choosing OAuth is the exception. On an edit that is how you reach the
  // Authorize button, and a connection whose token still works must not lose
  // it just because the admin opened the widget: an abandoned popup would
  // leave the connection strictly worse than before, with no undo. The
  // credential is replaced when a new login actually completes.
  const changeAuthMode = (next) => {
    if (!next || next === authMode) return
    setAuthModeOverride(next)
    if (next === 'oauth') return
    setMcpOAuthFlowId(null)
    headerKeys(secret).forEach(forget)
    Object.keys(stagedSecrets)
      .filter((k) => k.startsWith(HEADER_PREFIX))
      .forEach(forget)
    setSetting(ENV.auth, authEnvValue(next))
  }

  // MCP_AUTH must agree with the widget the admin is looking at, and a
  // connection saved before the setting existed has none at all. Written from
  // an effect rather than during render so the store is never mutated mid-render.
  const currentAuthEnv = readSetting(ENV.auth).trim()
  const wantedAuthEnv = stdio ? null : authEnvValue(authMode)
  useEffect(() => {
    if (wantedAuthEnv && currentAuthEnv !== wantedAuthEnv) {
      setSetting(ENV.auth, wantedAuthEnv)
    }
    // setSetting closes over replaceSecret, which is stable in zustand.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wantedAuthEnv, currentAuthEnv])

  // ---- OAuth -----------------------------------------------------------

  const finishError = (message, details) => {
    setBusy(false)
    setAuthError(message)
    showSnackbar({
      level: 'error',
      text: 'MCP authorization failed',
      description: message,
      details,
    })
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
      // The header is the token as it stands now; the flow id is what lets the
      // gateway adopt this login into a grant it can renew from the refresh
      // token (services.AdoptMCPOAuthGrant). Saving only the header leaves a
      // credential that dies at the provider's TTL.
      replaceSecret(AUTHORIZATION_KEY, encodeSecretValue(data.authorization_header))
      setMcpOAuthFlowId(flowId)
      setBusy(false)
      setAuthError(null)
      showSnackbar({
        level: 'success',
        text: 'MCP connection authorized',
        description: 'Save the connection to finish attaching the login.',
      })
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
      if (outcome.ok) redeemToken(outcome.flowId)
      else {
        const reason = outcome.reason ? `: ${outcome.reason.replace(/_/g, ' ')}` : ''
        finishError(`Authorization denied${reason}`)
      }
    }, POPUP_POLL_MS)
  }

  const authorize = async () => {
    const serverUrl = remoteUrl.trim()
    if (!serverUrl) {
      showSnackbar({
        level: 'error',
        text: 'Enter the MCP server URL before authorizing',
      })
      return
    }
    setBusy(true)
    setAuthError(null)
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
    setMcpOAuthFlowId(null)
    headerKeys(secret).forEach(forget)
    Object.keys(stagedSecrets)
      .filter((k) => k.startsWith(HEADER_PREFIX))
      .forEach(forget)
    setAuthError(null)
  }

  // ---- render ----------------------------------------------------------

  const serverLabel = entry?.name || 'Custom / self-hosted'

  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={4}>MCP Server</Title>
        <Text size="sm" c="dimmed">
          {catalog.status === 'ready' && entry
            ? `Recognized as the catalog server "${serverLabel}".`
            : 'Configured as a custom or self-hosted server.'}
        </Text>
        {entry?.description && (
          <Text size="sm" c="dimmed">
            {entry.description}
          </Text>
        )}
        {entry?.notes && (
          <Text size="sm" c="yellow.8">
            {entry.notes}
          </Text>
        )}

        <Select
          label="Transport"
          data={TRANSPORTS.map((t) => ({ value: t.value, label: t.label }))}
          value={transport}
          onChange={changeTransport}
          allowDeselect={false}
        />

        {stdio ? (
          <Stack gap="sm">
            {/* The command lives in the connection's command ARRAY
                (AgentConnectionParams.CmdList), not an env var — that is what
                the agent spawns. Edited as a plain command line and split on
                whitespace, the way the create form does it. */}
            <TextInput
              label="Command"
              required
              placeholder="e.g. npx -y @modelcontextprotocol/server-filesystem /data"
              value={commandLine}
              onChange={(e) =>
                setDraft({
                  command: e.currentTarget.value.split(/\s+/).filter(Boolean),
                })
              }
            />
            <Text size="sm" c="dimmed">
              {transport === 'client-stdio'
                ? 'Each user runs this command on their own machine through `hoop connect`, so the server sees their filesystem and their credentials. Every tool call is still inspected by Hoop before it reaches them.'
                : 'The agent runs this command as a child process. Add secrets it needs as environment variables below.'}
            </Text>
          </Stack>
        ) : (
          <PredefinedFields
            connection={connection}
            fields={URL_FIELD}
            availableSources={availableSources}
            forceNewState={forceNewState}
            connectionMethod={connectionMethod}
            hideRoleInfo={hideRoleInfo}
          />
        )}
      </Stack>

      {/* Authorization — remote transports only. A stdio child authenticates
          through its own environment and never sees an HTTP header. */}
      {!stdio && (
        <Paper withBorder radius="md" p="md">
          <Stack gap="md">
            <Stack gap={4}>
              <Title order={5}>MCP Authorization</Title>
              <Text size="sm" c="dimmed">
                How Hoop authenticates to this server. Every user of this
                connection shares the credential configured here.
              </Text>
            </Stack>

            {/* A dropdown with one option is a decision the admin does not
                have: where the provider accepts one credential, say which. */}
            {authOptions.length > 1 ? (
              <Select
                label="Authentication method"
                data={authOptions.map((o) => ({ value: o.value, label: o.label }))}
                value={authMode}
                onChange={changeAuthMode}
                allowDeselect={false}
              />
            ) : (
              <Text size="sm" fw={500}>
                {authOptions[0]?.label}
              </Text>
            )}

            {authMode === 'none' && (
              <Text size="sm" c="dimmed">
                No credential is sent. Public servers, and servers that
                authenticate by IP, need nothing here.
              </Text>
            )}

            {authMode === 'passthrough' && (
              <Stack gap="xs">
                <Text size="sm" c="dimmed">
                  No credential is stored on this connection. Each user&apos;s MCP
                  client sends its own token, and Hoop forwards it to the server —
                  so the server sees who is calling, and access follows each
                  user&apos;s own permissions.
                </Text>
                <Text size="sm" c="dimmed">
                  Users set this header in their MCP client configuration:
                </Text>
                <Text size="xs" c="dimmed" ff="monospace" style={{ wordBreak: 'break-all' }}>
                  X-Hoop-Upstream-Authorization: Bearer &lt;their token&gt;
                </Text>
              </Stack>
            )}

            {/* The credential rides in the header the provider expects, which
                is NOT always Authorization (context7 wants CONTEXT7_API_KEY,
                google-maps wants X-Goog-Api-Key). The name is read off the
                connection's own key so an existing token stays editable in
                place instead of being retyped under a second header.

                Required only when the admin picked this mode during THIS
                session, i.e. is deliberately configuring a credential. The
                create form marks it required unconditionally, which is right
                there — nothing exists yet. Here the connection already exists
                and works, so an unconditional `required` on a field the admin
                never touched fails form validation on mount and blocks Save
                for every unrelated edit: changing a tool policy would demand
                re-pasting a secret the screen deliberately never shows. */}
            {authMode === 'static' && (
              <Stack gap="xs">
                <PredefinedFields
                  connection={connection}
                  fields={[
                    {
                      key: credentialHeaderName,
                      envKey: credentialKey,
                      label: `${credentialHeaderName} value`,
                      required: authModeOverride === 'static',
                      type: 'password',
                      placeholder: 'Paste the API key or token issued by the provider',
                    },
                  ]}
                  availableSources={availableSources}
                  forceNewState={forceNewState}
                  connectionMethod={connectionMethod}
                  hideRoleInfo={hideRoleInfo}
                />
                <Text size="sm" c="dimmed">
                  {`Sent to the server in the ${credentialHeaderName} header.`}
                </Text>
              </Stack>
            )}

            {authMode === 'oauth' && (
              <Stack gap="sm">
                {authorized ? (
                  <Group justify="space-between" align="center" gap="sm">
                    <Group gap={6} align="center">
                      <Check size={16} color="var(--mantine-color-green-8)" />
                      <Text size="sm" fw={500} c="green.8">
                        {granted && !pendingFlowId
                          ? 'Authorized — token renewed automatically'
                          : pendingFlowId
                            ? 'Authorized — save to finish attaching the login'
                            : 'Authorized — access token stored'}
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
                  <>
                    {/* Pre-registered client credentials. Left blank, Hoop
                        registers a client dynamically (RFC 7591) — which
                        plenty of authorization servers do not offer (GitHub
                        publishes no registration_endpoint), and for those an
                        OAuth app the admin created by hand is the only way in.
                        Auth-flow inputs only: never persisted, because every
                        credential here would become a connection env var. */}
                    <TextInput
                      label="Client ID (optional)"
                      placeholder="Leave blank to register automatically"
                      value={clientId}
                      onChange={(e) => setClientId(e.currentTarget.value)}
                    />
                    <PasswordInput
                      label="Client Secret (optional)"
                      placeholder="Only if the provider issued one"
                      value={clientSecret}
                      onChange={(e) => setClientSecret(e.currentTarget.value)}
                    />
                    <Button
                      w="fit-content"
                      leftSection={<ShieldCheck size={16} />}
                      loading={busy}
                      onClick={authorize}
                    >
                      {busy ? 'Authorizing…' : 'Authorize with MCP'}
                    </Button>
                    <Text size="sm" c="dimmed">
                      Hoop discovers the server&apos;s authorization server, logs
                      in, and stores the resulting access token. Register the
                      callback below as the redirect URI of your OAuth app.
                    </Text>
                    <Text size="xs" c="dimmed" ff="monospace" style={{ wordBreak: 'break-all' }}>
                      {`${window.location.origin}/api/mcp-oauth/callback`}
                    </Text>
                    {authError && (
                      <Text size="sm" c="red">
                        {authError}
                      </Text>
                    )}
                  </>
                )}
              </Stack>
            )}
          </Stack>
        </Paper>
      )}

      {/* Tool policy — the reason this connection type exists: control that
          names a tool. Empty means no restriction; deny wins over allow. */}
      <Paper withBorder radius="md" p="md">
        <Stack gap="md">
          <Stack gap={4}>
            <Title order={5}>Tool policy</Title>
            <Text size="sm" c="dimmed">
              Comma-separated tool name patterns, e.g. read_*, create_issue.
              Denied tools are removed from the server&apos;s tool list before the
              model sees them.
            </Text>
          </Stack>
          <TextInput
            label="Allowed tools"
            placeholder="Leave empty to allow every tool"
            value={readSetting(ENV.allowedTools)}
            onChange={(e) => setSetting(ENV.allowedTools, e.currentTarget.value)}
          />
          <TextInput
            label="Denied tools"
            placeholder="e.g. delete_*, admin_*"
            value={readSetting(ENV.deniedTools)}
            onChange={(e) => setSetting(ENV.deniedTools, e.currentTarget.value)}
          />
          <TextInput
            label="Tools requiring approval"
            placeholder="e.g. create_issue"
            value={readSetting(ENV.approvalTools)}
            onChange={(e) => setSetting(ENV.approvalTools, e.currentTarget.value)}
          />
          <Text size="sm" c="yellow.8">
            Approval routing is not available yet — matched tools are denied
            until it ships.
          </Text>
        </Stack>
      </Paper>

      <Paper withBorder radius="md" p="md">
        <Stack gap="md">
          <Title order={5}>Limits</Title>
          <NumberInput
            label="Max tool calls per session"
            placeholder="Leave empty for no limit"
            min={0}
            value={readSetting(ENV.maxCalls)}
            onChange={(v) => setSetting(ENV.maxCalls, v === '' || v == null ? '' : String(v))}
          />
          <NumberInput
            label="Max result size (KB)"
            placeholder="Leave empty for no limit"
            min={0}
            value={readSetting(ENV.maxResultKb)}
            onChange={(v) => setSetting(ENV.maxResultKb, v === '' || v == null ? '' : String(v))}
          />
          <Select
            label="When a tool changes mid-session"
            data={RUG_PULL_MODES.map((m) => ({ value: m.value, label: m.label }))}
            value={readSetting(ENV.onRugPull).trim() || 'kill'}
            onChange={(v) => v && setSetting(ENV.onRugPull, v)}
            allowDeselect={false}
          />
          {/* Both default ON: the agent's policy treats "unset" as the secure
              default, so only an explicit false opens the gate. */}
          <ToggleSection
            title="Block sampling requests"
            description="Prevent the server from driving your LLM through sampling/createMessage."
            checked={readBool(ENV.blockSampling, true)}
            onChange={(on) => setSetting(ENV.blockSampling, on ? 'true' : 'false')}
          />
          <ToggleSection
            title="Block elicitation requests"
            description="Prevent the server from prompting your users with its own dialogs."
            checked={readBool(ENV.blockElicitation, true)}
            onChange={(on) => setSetting(ENV.blockElicitation, on ? 'true' : 'false')}
          />
        </Stack>
      </Paper>

      {/* A stdio server's secrets reach its child process through the MCPENV_
          carve-out; a remote server's travel as outbound HTTP headers. Same
          editor, different namespace — putting one in the other's place either
          leaks a subprocess secret onto the wire or hides an HTTP header in a
          process environment. */}
      {stdio ? (
        <HttpHeaders
          connection={connection}
          availableSources={availableSources}
          hideRoleInfo={hideRoleInfo}
          prefix={MCPENV_PREFIX}
          title="Server environment variables"
          description="Passed to the MCP server process. Hoop adds the MCPENV_ prefix on the wire and strips it before the child sees it."
          keyPlaceholder="FIGMA_TOKEN"
          addLabel="Add variable"
        />
      ) : (
        <HttpHeaders
          connection={connection}
          availableSources={availableSources}
          excludeKeys={[credentialKey]}
          hideRoleInfo={hideRoleInfo}
        />
      )}

      {!stdio && <AllowInsecureSsl connection={connection} />}
      <AgentSelector />
    </Stack>
  )
}
