import { createStore } from 'zustand/vanilla'
import { credentialsSlice } from '@/pages/Roles/Configure/utils/credentialsSlice'
import {
  federationSlice,
  INITIAL_FEDERATION,
  INITIAL_FEDERATION_TEST,
} from '@/pages/Roles/Configure/utils/federationSlice'
import {
  decodeForDisplay,
  decodeSecretValue,
  encodeSecretForSource,
  encodeSecretValue,
  PLACEHOLDER_KEY_RE,
  sourceFromEncodedValue,
  SOURCES,
} from '@/pages/Roles/Configure/utils/secretsCodec'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'

const PASS_KEY = 'envvar:PASS'
const SSH_KEY_KEY = 'envvar:AUTHORIZED_SERVER_KEYS'
const AWS_IAM_KEYS = new Set(['envvar:USER', PASS_KEY])
const INSECURE_KEY = 'envvar:INSECURE'
const SSH_PRIVATE_KEY = 'filesystem:SSH_PRIVATE_KEY'
const REMOTE_URL_KEY = 'envvar:REMOTE_URL'
export const MCP_OAUTH_KEY = 'envvar:HEADER_AUTHORIZATION'

const HTTP_PROXY_SUBTYPES = new Set(['httpproxy', 'grafana', 'kibana', 'claude-code', 'mcp', 'mcpproxy'])

// Settings and the OAuth token are values, never secret references: CLJS
// prefixed them under Secrets Manager (Q37).
function manualOnly(key) {
  return (
    key.startsWith('filesystem:') ||
    key === INSECURE_KEY ||
    key === MCP_OAUTH_KEY ||
    key.startsWith('envvar:MCP_') ||
    key.startsWith('envvar:GCP_')
  )
}

function sourceFor(key, method, provider) {
  if (manualOnly(key)) return SOURCES.MANUAL
  if (method === CONNECTION_METHODS.SECRETS_MANAGER) return provider
  if (method === CONNECTION_METHODS.AWS_IAM && AWS_IAM_KEYS.has(key)) return SOURCES.AWS_IAM_ROLE
  return SOURCES.MANUAL
}

// A method or provider change moves every typed value to the new default
// source, as update-role-credentials-source does in the CLJS wizard. A value
// the form wrote itself stays a value: CLJS made a vault path of it (Q39).
function reencode(stagedSecrets, method, provider, formValues, fieldSources) {
  const next = {}
  for (const [key, change] of Object.entries(stagedSecrets)) {
    const plain = decodeForDisplay(change.value)
    const kept = formValues[key] === plain && !fieldSources[key]
    const source = kept ? SOURCES.MANUAL : sourceFor(key, method, provider)
    next[key] = { ...change, value: encodeSecretForSource(plain, source) }
  }
  return next
}

// An OAuth token authenticates only to the server it was issued for. CLJS kept
// it when the URL changed (Q38).
function dropTokenOffServer(state) {
  if (!state.mcpOAuthUrl || remoteUrlOf(state) === state.mcpOAuthUrl) return {}
  return { stagedSecrets: without(state.stagedSecrets, MCP_OAUTH_KEY), mcpOAuthFlowId: null, mcpOAuthUrl: null }
}

function remoteUrlOf(state) {
  return decodeForDisplay(state.stagedSecrets[REMOTE_URL_KEY]?.value).trim()
}

function without(stagedSecrets, key) {
  const next = { ...stagedSecrets }
  delete next[key]
  return next
}

// PredefinedFields stages PASS as `authtoken` in AWS IAM mode. The admin's own
// PASS waits in `parkedPass` and comes back when the role leaves AWS IAM.
function swapPass(state, method) {
  const toAwsIam = method === CONNECTION_METHODS.AWS_IAM
  const pass = state.stagedSecrets[PASS_KEY]
  const forced = pass && sourceFromEncodedValue(pass.value) === SOURCES.AWS_IAM_ROLE
  if (toAwsIam && pass && !forced) {
    return { stagedSecrets: without(state.stagedSecrets, PASS_KEY), parkedPass: pass }
  }
  if (!toAwsIam && forced) {
    const stagedSecrets = state.parkedPass
      ? { ...state.stagedSecrets, [PASS_KEY]: state.parkedPass }
      : without(state.stagedSecrets, PASS_KEY)
    return { stagedSecrets, parkedPass: null }
  }
  return { stagedSecrets: state.stagedSecrets, parkedPass: state.parkedPass }
}

// The values the CLJS forms write on their first render: the HTTP proxy family
// always sends INSECURE, Claude Code the Anthropic URL, and the MCP Gateway its
// transport and auth mode, which the agent requires.
function initialValues(subtype) {
  if (!HTTP_PROXY_SUBTYPES.has(subtype)) return {}
  const values = { [INSECURE_KEY]: 'false' }
  if (subtype === 'claude-code') values[REMOTE_URL_KEY] = 'https://api.anthropic.com'
  if (subtype === 'mcpproxy') {
    values['envvar:MCP_TRANSPORT'] = 'streamable-http'
    values['envvar:MCP_AUTH'] = 'static'
  }
  return values
}

function staged(values) {
  return Object.fromEntries(
    Object.entries(values).map(([key, value]) => [key, { action: 'new', value: encodeSecretValue(value) }]),
  )
}

// config->json: every key is upper-cased, except a header of the HTTP proxy family.
function wireKey(key, subtype) {
  const colon = key.indexOf(':') + 1
  const name = key.slice(colon)
  if (HTTP_PROXY_SUBTYPES.has(subtype) && name.startsWith('HEADER_')) return key
  return key.slice(0, colon) + name.toUpperCase()
}

// One store per wizard role. The Configure Role credential renderers and the
// federation form read it through CredentialsStoreProvider, so each role has
// its own federation form (Q20). The secret map is empty, so every field is new.
// drafts.subtype turns ssh into ssh-local; drafts.command holds the typed arguments.
export function createRoleCredentialsStore({ type, subtype }) {
  const formValues = initialValues(subtype)
  return createStore((set, get) => {
    const slice = credentialsSlice(set)
    return {
    createMode: true,
    connection: { type, subtype, secret: {} },
    drafts: { subtype, command: [] },
    stagedSecrets: staged(formValues),
    formValues,
    fieldSources: {},
    renames: {},
    hideRoleInfo: false,
    parkedPass: null,
    mcpOAuthFlowId: null,
    mcpOAuthUrl: null,
    formState: {},
    federation: INITIAL_FEDERATION,
    federationTest: INITIAL_FEDERATION_TEST,
    ...slice,
    ...federationSlice(set, get),
    replaceSecret: (key, base64Value) => {
      slice.replaceSecret(key, base64Value)
      if (key === REMOTE_URL_KEY) set(dropTokenOffServer)
    },
    cancelSecretChange: (key) => {
      slice.cancelSecretChange(key)
      if (key === REMOTE_URL_KEY) set(dropTokenOffServer)
    },
    // A value the form filled in by itself, such as a catalog server's URL.
    setFormValue: (key, plain) => {
      set((state) => ({
        stagedSecrets: { ...state.stagedSecrets, [key]: { action: 'new', value: encodeSecretValue(plain) } },
        formValues: { ...state.formValues, [key]: plain },
      }))
      if (key === REMOTE_URL_KEY) set(dropTokenOffServer)
    },
    setDraft: (patch) => set((state) => ({ drafts: { ...state.drafts, ...patch } })),
    setMcpOAuthFlowId: (mcpOAuthFlowId) => set(mcpOAuthFlowId ? { mcpOAuthFlowId } : { mcpOAuthFlowId: null, mcpOAuthUrl: null }),
    // Stages a redeemed login only while the form still names its server.
    adoptMcpOAuth: (serverUrl, header, flowId) => {
      if (remoteUrlOf(get()) !== serverUrl.trim()) return false
      set((state) => ({
        stagedSecrets: { ...state.stagedSecrets, [MCP_OAUTH_KEY]: { action: 'new', value: encodeSecretValue(header) } },
        mcpOAuthFlowId: flowId,
        mcpOAuthUrl: serverUrl.trim(),
      }))
      return true
    },
    setFormState: (key, value) => set((state) => ({ formState: { ...state.formState, [key]: value } })),
    // The SSH form hides the credential of the other auth method, so it is not
    // sent. CLJS sent a password typed before a switch to key.
    chooseSshAuth: (method) => get().cancelSecretChange(method === 'password' ? SSH_KEY_KEY : PASS_KEY),
    applyMethod: (method, provider) =>
      set((state) => {
        const { stagedSecrets, parkedPass } = swapPass(state, method)
        return { stagedSecrets: reencode(stagedSecrets, method, provider, state.formValues, state.fieldSources), fieldSources: {}, parkedPass }
      }),
    }
  })
}

// The role's `secret` for POST /resources: every typed value under its renamed
// key. Blank values and unnamed rows are left out, and the SSH private key file
// gets a trailing newline, as config->json does.
export function secretOf(credentialsStore) {
  const { stagedSecrets, renames, connection } = credentialsStore.getState()
  const secret = {}
  for (const [key, change] of Object.entries(stagedSecrets)) {
    if (change.action !== 'new' || PLACEHOLDER_KEY_RE.test(renames[key] ?? key)) continue
    const name = wireKey(renames[key] ?? key, connection.subtype)
    if (decodeForDisplay(change.value) === '') continue
    secret[name] =
      name === SSH_PRIVATE_KEY ? encodeSecretValue(decodeSecretValue(change.value) + '\n') : change.value
  }
  return secret
}
