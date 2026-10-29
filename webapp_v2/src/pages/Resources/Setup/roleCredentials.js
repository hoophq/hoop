import { createStore } from 'zustand/vanilla'
import { credentialsSlice } from '@/pages/Roles/Configure/utils/credentialsSlice'
import {
  decodeForDisplay,
  encodeSecretForSource,
  sourceFromEncodedValue,
  SOURCES,
} from '@/pages/Roles/Configure/utils/secretsCodec'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'

const PASS_KEY = 'envvar:PASS'
const AWS_IAM_KEYS = new Set(['envvar:USER', PASS_KEY])

function sourceFor(key, method, provider) {
  if (key.startsWith('filesystem:')) return SOURCES.MANUAL
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

// One store per wizard role. The Configure Role credential renderers read it
// through CredentialsStoreProvider. The secret map is empty, so every field is new.
export function createRoleCredentialsStore({ type, subtype }) {
  return createStore((set) => ({
    createMode: true,
    connection: { type, subtype, secret: {} },
    stagedSecrets: {},
    fieldSources: {},
    renames: {},
    hideRoleInfo: false,
    parkedPass: null,
    ...credentialsSlice(set),
    applyMethod: (method, provider) =>
      set((state) => {
        const { stagedSecrets, parkedPass } = swapPass(state, method)
        return { stagedSecrets: reencode(stagedSecrets, method, provider), fieldSources: {}, parkedPass }
      }),
  }))
}

// The role's `secret` for POST /resources: every typed value, blank ones left
// out as config->json does.
export function secretOf(credentialsStore) {
  const { stagedSecrets } = credentialsStore.getState()
  return Object.fromEntries(
    Object.entries(stagedSecrets)
      .filter(([, change]) => change.action === 'new' && decodeForDisplay(change.value) !== '')
      .map(([key, change]) => [key, change.value]),
  )
}
