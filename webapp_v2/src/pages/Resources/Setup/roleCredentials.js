import { createStore } from 'zustand/vanilla'
import { credentialsSlice } from '@/pages/Roles/Configure/utils/credentialsSlice'
import {
  decodeForDisplay,
  encodeSecretForSource,
  sourceFromEncodedValue,
  SOURCES,
} from '@/pages/Roles/Configure/utils/secretsCodec'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'

const AWS_IAM_KEYS = new Set(['envvar:USER', 'envvar:PASS'])

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
    // AWS IAM stages PASS as `authtoken`: it is not a password to keep.
    const forcedPass =
      key === 'envvar:PASS' && sourceFromEncodedValue(change.value) === SOURCES.AWS_IAM_ROLE
    if (forcedPass && method !== CONNECTION_METHODS.AWS_IAM) continue
    const plain = decodeForDisplay(change.value)
    next[key] = { ...change, value: encodeSecretForSource(plain, sourceFor(key, method, provider)) }
  }
  return next
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
    ...credentialsSlice(set),
    applyMethod: (method, provider) =>
      set((state) => ({
        stagedSecrets: reencode(state.stagedSecrets, method, provider),
        fieldSources: {},
      })),
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
