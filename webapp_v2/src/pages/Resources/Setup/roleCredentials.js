import { createStore } from 'zustand/vanilla'
import { credentialsSlice } from '@/pages/Roles/Configure/utils/credentialsSlice'
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
// source, as update-role-credentials-source does in the CLJS wizard.
function reencode(stagedSecrets, method, provider) {
  const next = {}
  for (const [key, change] of Object.entries(stagedSecrets)) {
    const plain = decodeForDisplay(change.value)
    next[key] = { ...change, value: encodeSecretForSource(plain, sourceFor(key, method, provider)) }
  }
  return next
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
function initialSecrets(subtype) {
  if (!HTTP_PROXY_SUBTYPES.has(subtype)) return {}
  const values = { [INSECURE_KEY]: 'false' }
  if (subtype === 'claude-code') values['envvar:REMOTE_URL'] = 'https://api.anthropic.com'
  if (subtype === 'mcpproxy') {
    values['envvar:MCP_TRANSPORT'] = 'streamable-http'
    values['envvar:MCP_AUTH'] = 'static'
  }
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

// One store per wizard role. The Configure Role credential renderers read it
// through CredentialsStoreProvider. The secret map is empty, so every field is new.
// drafts.subtype turns ssh into ssh-local; drafts.command holds the typed arguments.
export function createRoleCredentialsStore({ type, subtype }) {
  return createStore((set, get) => ({
    createMode: true,
    connection: { type, subtype, secret: {} },
    drafts: { subtype, command: [] },
    stagedSecrets: initialSecrets(subtype),
    fieldSources: {},
    renames: {},
    hideRoleInfo: false,
    parkedPass: null,
    mcpOAuthFlowId: null,
    ...credentialsSlice(set),
    setDraft: (patch) => set((state) => ({ drafts: { ...state.drafts, ...patch } })),
    setMcpOAuthFlowId: (mcpOAuthFlowId) => set({ mcpOAuthFlowId }),
    // The SSH form hides the credential of the other auth method, so it is not
    // sent. CLJS sent a password typed before a switch to key.
    chooseSshAuth: (method) => get().cancelSecretChange(method === 'password' ? SSH_KEY_KEY : PASS_KEY),
    applyMethod: (method, provider) =>
      set((state) => {
        const { stagedSecrets, parkedPass } = swapPass(state, method)
        return { stagedSecrets: reencode(stagedSecrets, method, provider), fieldSources: {}, parkedPass }
      }),
  }))
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
