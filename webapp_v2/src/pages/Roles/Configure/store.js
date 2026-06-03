import { create } from 'zustand'
import { connectionsService } from '@/services/connections'
import { guardrailsService } from '@/services/guardrails'
import { jiraTemplatesService } from '@/services/jiraTemplates'
import { attributesService } from '@/services/attributes'
import { connectionTagsService } from '@/services/connectionTags'
import { userGroupsService } from '@/services/userGroups'
import {
  decodeForDisplay,
  decodeSecretValue,
  encodeSecretForSource,
  isSecretReference,
  PLACEHOLDER_KEY_RE,
  sourceFromEncodedValue,
} from './utils/secretsCodec'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'

// Page-local store for Configure Role. The staged-secrets contract is
// the only non-obvious bit:
//   stagedSecrets[key] = { action: 'replace' | 'delete' | 'new', value?: string }
//   - 'replace' overrides an existing inline secret (value is base64-encoded).
//   - 'delete'  removes an existing key (custom connection type only).
//   - 'new'     adds a brand-new custom-type secret (key + value).

// Schema for the draft fields the form edits. One entry per field that
// round-trips between `connection` (server) and `drafts` (form state).
// Adding a new connection-level field: append one entry here — no
// changes needed in draftsFromConnection / buildDraftsPatch.
//
// Field options:
//   default     fallback used when the connection has no value
//   compare     equality predicate for the dirty check (default: ===)
//   transform   pre-process before compare/patch (e.g. prune blanks)
//   nullable    true → use `??` coalescing (preserves 0/false);
//               false → `||` (replaces falsy with default)
//
// `redact_enabled` is a UI-only flag; the wire toggles via
// `redact_types: []` for "off". Handled as a compound special-case
// below the per-field loop in buildDraftsPatch.
const DRAFT_FIELDS = {
  attributes: { default: [], compare: arraysEqual },
  connection_tags: { default: {}, compare: objectsEqual, transform: pruneEmptyTags },
  access_mode_exec: { default: 'enabled' },
  access_mode_runbooks: { default: 'enabled' },
  access_mode_connect: { default: 'enabled' },
  access_schema: { default: 'enabled' },
  guardrail_rules: { default: [], compare: arraysEqual },
  jira_issue_template_id: { default: '' },
  // Drop blank rows so the payload doesn't carry junk like `[""]` —
  // the placeholder UX keeps an empty input visible at the bottom and
  // the user can also clear the first row, both of which leave a
  // whitespace-only entry in the draft. Mirrors CLJS process_form's
  // (filterv #(not (str/blank? %))) on mandatory-metadata-fields.
  mandatory_metadata_fields: {
    default: [],
    compare: arraysEqual,
    transform: (xs) =>
      (xs || []).filter((s) => typeof s === 'string' && s.trim() !== ''),
  },
  reviewers: { default: [], compare: arraysEqual },
  min_review_approvals: { default: null, nullable: true },
  force_approve_groups: { default: [], compare: arraysEqual },
  access_max_duration: { default: null, nullable: true },
  agent_id: { default: '' },
  command: { default: [], compare: arraysEqual },
}

function freshDrafts() {
  const out = {}
  for (const [key, spec] of Object.entries(DRAFT_FIELDS)) {
    // Fresh copies for mutable defaults so callers can't share state.
    out[key] = Array.isArray(spec.default)
      ? []
      : spec.default !== null && typeof spec.default === 'object'
        ? {}
        : spec.default
  }
  out.redact_enabled = false
  out.redact_types = []
  return out
}

function draftsFromConnection(conn) {
  if (!conn) return freshDrafts()
  const drafts = {}
  for (const [key, spec] of Object.entries(DRAFT_FIELDS)) {
    const value = conn[key]
    drafts[key] = spec.nullable
      ? (value ?? spec.default)
      : (value || spec.default)
  }
  drafts.redact_enabled = !!conn.redact_enabled
  drafts.redact_types = conn.redact_types || []
  return drafts
}

// Empty placeholder rows (the auto-added blank row that keeps
// Environment variables / Configuration files from looking empty)
// live in stagedSecrets as 'new' entries with no value and a
// generated key pattern (PLACEHOLDER_KEY_RE in utils/secretsCodec.js).
// They never reach the backend and don't count toward dirty.
//
// Filled-but-unnamed placeholders are handled separately at save time
// (save() throws a validation error) so the user gets a clear message
// instead of a silent content drop.
function isPlaceholderEntry(key, change) {
  return change?.action === 'new' && !change.value && PLACEHOLDER_KEY_RE.test(key)
}

// If both probe results are in, return a patch that closes out the
// test duration. Otherwise return an empty patch so set(...) is a no-op
// for the duration field.
function maybeSettleDuration({ startedAt, agentStatus, connectionStatus }) {
  const bothDone =
    agentStatus !== 'checking' && connectionStatus !== 'checking'
  if (!bothDone) return {}
  return { testDurationMs: Date.now() - startedAt }
}

// Shallow equality for arrays (order-sensitive) and plain objects (key/value).
function arraysEqual(a, b) {
  if (a === b) return true
  if (!Array.isArray(a) || !Array.isArray(b)) return false
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false
  return true
}

function objectsEqual(a, b) {
  if (a === b) return true
  const ka = Object.keys(a || {})
  const kb = Object.keys(b || {})
  if (ka.length !== kb.length) return false
  for (const k of ka) {
    if (a[k] !== b[k]) return false
  }
  return true
}

// Drops entries with empty key or empty value from a tags map. Mirrors
// CLJS process_form.cljs's :filter-valid-tags so the wire payload stays
// in sync with what the backend expects.
function pruneEmptyTags(tags) {
  const out = {}
  for (const [k, v] of Object.entries(tags || {})) {
    if (!k || !String(k).trim()) continue
    if (v == null || !String(v).trim()) continue
    out[k] = v
  }
  return out
}

// Returns only the keys of `drafts` that differ from the connection's
// current value, formatted in the shape PATCH /connections expects.
// Per-field comparison + transform comes from DRAFT_FIELDS.
function buildDraftsPatch(drafts, baseline) {
  const patch = {}
  for (const [key, spec] of Object.entries(DRAFT_FIELDS)) {
    const compare = spec.compare || ((a, b) => a === b)
    let draftValue = drafts[key]
    let baselineValue = baseline[key]
    if (spec.transform) {
      draftValue = spec.transform(draftValue)
      baselineValue = spec.transform(baselineValue)
    }
    if (!compare(draftValue, baselineValue)) {
      patch[key] = draftValue
    }
  }
  // redact_enabled is UI-only — the wire toggles masking via
  // `redact_types: []` for off. Translate at save time.
  const desiredRedact = drafts.redact_enabled ? drafts.redact_types : []
  const baselineRedact = baseline.redact_enabled ? baseline.redact_types : []
  if (!arraysEqual(desiredRedact, baselineRedact)) {
    patch.redact_types = desiredRedact
  }
  return patch
}

const initialState = {
  connection: null,
  loading: false,
  error: null,
  saving: false,
  deleting: false,
  testing: false,
  testResult: null,
  testModalOpen: false,
  testAgentStatus: 'checking',
  testConnectionStatus: 'checking',
  testStartedAt: null,
  testDurationMs: null,
  stagedSecrets: {},
  // Per-field source identifier (mirrors :connection-setup/field-source
  // in CLJS). Empty by default; seeded from the connection on load.
  fieldSources: {},
  // { originalKey: newKey } for rows whose Key field was renamed but
  // whose React row position should stay put. Translated into
  // delete+replace pairs by buildSecretsPatch on save.
  renames: {},
  drafts: freshDrafts(),
  baseline: freshDrafts(), // snapshot of drafts as loaded; used for diffing
  guardrailsList: [],
  jiraTemplatesList: [],
  attributesList: [],
  // Pool of every tag (key+value pair) that has been used at least once
  // anywhere in the org. The Tags input derives autocompleted keys and
  // per-key value suggestions from this list.
  connectionTagsPool: [],
  userGroupsList: [],
  auxLoading: false,
}

export const useConfigureRoleStore = create((set, get) => ({
  ...initialState,

  loadConnection: async (nameOrId) => {
    set({
      loading: true,
      error: null,
      connection: null,
      stagedSecrets: {},
      fieldSources: {},
      renames: {},
    })
    try {
      const data = await connectionsService.getConnection(nameOrId)
      const drafts = draftsFromConnection(data)
      const fieldSources = {}
      for (const [k, v] of Object.entries(data.secret || {})) {
        fieldSources[k] = sourceFromEncodedValue(v)
      }
      set({
        connection: data,
        drafts,
        baseline: drafts,
        fieldSources,
        loading: false,
      })
    } catch (err) {
      const message = err?.response?.data?.message || 'Failed to load connection.'
      set({ error: message, loading: false })
    }
  },

  loadAuxiliaryData: async () => {
    set({ auxLoading: true })
    try {
      const [guardrails, jiraTemplates, attributesRes, connectionTags, userGroups] = await Promise.allSettled([
        guardrailsService.list(),
        jiraTemplatesService.list(),
        attributesService.list(),
        connectionTagsService.list(),
        userGroupsService.list(),
      ])
      // /guardrails and /integrations/jira/issuetemplates return bare arrays
      // (the service unwraps res.data for us). /attributes returns a
      // paginated envelope { data: [...], pages: {...} } and the
      // existing attributesService leaves the axios response untouched,
      // so the array sits at value.data.data. /connection-tags returns
      // { items: [{ id, key, value, ... }] }.
      const attributesList =
        attributesRes.status === 'fulfilled'
          ? attributesRes.value?.data?.data || []
          : []
      const connectionTagsPool =
        connectionTags.status === 'fulfilled'
          ? connectionTags.value?.items || []
          : []
      const userGroupsList =
        userGroups.status === 'fulfilled' ? userGroups.value || [] : []
      set({
        guardrailsList:
          guardrails.status === 'fulfilled' ? guardrails.value || [] : [],
        jiraTemplatesList:
          jiraTemplates.status === 'fulfilled' ? jiraTemplates.value || [] : [],
        attributesList,
        connectionTagsPool,
        userGroupsList,
        auxLoading: false,
      })
    } catch {
      set({ auxLoading: false })
    }
  },

  setDraft: (patch) =>
    set((state) => ({ drafts: { ...state.drafts, ...patch } })),

  setMandatoryMetadataField: (index, value) =>
    set((state) => {
      const next = [...state.drafts.mandatory_metadata_fields]
      next[index] = value
      return { drafts: { ...state.drafts, mandatory_metadata_fields: next } }
    }),

  addMandatoryMetadataField: () =>
    set((state) => ({
      drafts: {
        ...state.drafts,
        mandatory_metadata_fields: [...state.drafts.mandatory_metadata_fields, ''],
      },
    })),

  removeMandatoryMetadataField: (index) =>
    set((state) => {
      const next = state.drafts.mandatory_metadata_fields.filter((_, i) => i !== index)
      return { drafts: { ...state.drafts, mandatory_metadata_fields: next } }
    }),

  setTag: (key, value) =>
    set((state) => ({
      drafts: {
        ...state.drafts,
        connection_tags: { ...state.drafts.connection_tags, [key]: value },
      },
    })),

  removeTag: (key) =>
    set((state) => {
      const next = { ...state.drafts.connection_tags }
      delete next[key]
      return { drafts: { ...state.drafts, connection_tags: next } }
    }),

  replaceSecret: (key, base64Value) => {
    set((state) => {
      const isExisting = state.connection?.secret && key in state.connection.secret
      return {
        stagedSecrets: {
          ...state.stagedSecrets,
          [key]: {
            action: isExisting ? 'replace' : 'new',
            value: base64Value,
          },
        },
      }
    })
  },

  // Per-field source (manual / vault-kv1 / vault-kv2 / aws-secrets-manager).
  // Drives the source-selector adornment when the connection method is
  // Secrets Manager. Changing the source for a field already-staged with
  // a new value re-encodes it so the prefix matches.
  setFieldSource: (key, source) => {
    set((state) => {
      const staged = state.stagedSecrets[key]
      const nextSources = { ...state.fieldSources, [key]: source }
      // If the user already typed a new value, re-encode under the new
      // prefix so save() sends the right thing.
      let nextStaged = state.stagedSecrets
      if (staged && staged.value) {
        // staged.value is base64-encoded; decode then re-encode for the
        // new source. Empty staged values stay empty.
        const plain = (() => {
          try {
            const decoded = atob(staged.value)
            // strip any existing provider prefix so we don't double-up
            for (const prefix of ['_aws:', '_envjson:', '_vaultkv1:', '_vaultkv2:', '_aws_iam_rds:']) {
              if (decoded.startsWith(prefix)) return decoded.slice(prefix.length)
            }
            return decoded
          } catch {
            return ''
          }
        })()
        const reencoded =
          source === 'manual-input'
            ? btoa(plain)
            : btoa(
                ({
                  'vault-kv1': '_vaultkv1:',
                  'vault-kv2': '_vaultkv2:',
                  'aws-secrets-manager': '_aws:',
                }[source] || '') + plain,
              )
        nextStaged = {
          ...state.stagedSecrets,
          [key]: { ...staged, value: reencoded },
        }
      }
      return { fieldSources: nextSources, stagedSecrets: nextStaged }
    })
  },

  deleteSecret: (key) => {
    set((state) => ({
      stagedSecrets: { ...state.stagedSecrets, [key]: { action: 'delete' } },
    }))
  },

  cancelSecretChange: (key) => {
    set((state) => {
      const next = { ...state.stagedSecrets }
      delete next[key]
      // Drop any pending rename for the same key so cancelling a fresh
      // row doesn't leave a phantom rename around.
      const nextRenames = { ...state.renames }
      delete nextRenames[key]
      return { stagedSecrets: next, renames: nextRenames }
    })
  },

  // Rename the Key of a row. We always record the rename in the
  // `renames` map and leave the `stagedSecrets` entry under its
  // original key. That keeps the React `key={envKey}` on the rendered
  // row stable across renames so the row never unmounts mid-typing —
  // moving the entry between keys would lose focus on the next input
  // (Tab from Name to Value) and silently drop the user's typed value.
  // buildSecretsPatch translates the rename into delete-old +
  // replace-new on the wire for both 'new' and persisted entries.
  renameSecret: (originalKey, newKey) => {
    set((state) => {
      if (newKey === originalKey) {
        const renames = { ...state.renames }
        delete renames[originalKey]
        return { renames }
      }
      return { renames: { ...state.renames, [originalKey]: newKey } }
    })
  },

  clearStagedSecrets: () =>
    set({ stagedSecrets: {}, renames: {}, fieldSources: {} }),

  // Stages deletes for any persisted reference that doesn't belong to
  // the new method, so save() actually wipes them on the wire instead
  // of leaving stale prefixes that would re-derive the old method on
  // the next load.
  //
  // Compat table (mirrors deriveConnectionMethod):
  //   MANUAL          → wipe every reference prefix
  //   SECRETS_MANAGER → wipe `_aws_iam_rds:` only
  //   AWS_IAM         → wipe `_aws:` / `_vaultkv*:`; keep `_aws_iam_rds:`
  switchConnectionMethod: (newMethod) => {
    set((state) => {
      const stagedSecrets = {}
      const secrets = state.connection?.secret || {}
      for (const [key, encoded] of Object.entries(secrets)) {
        if (!encoded || !isSecretReference(encoded)) continue
        const plain = decodeSecretValue(encoded)
        const isIamRef = plain.startsWith('_aws_iam_rds:')
        let shouldDelete = false
        switch (newMethod) {
          case CONNECTION_METHODS.MANUAL:
            shouldDelete = true
            break
          case CONNECTION_METHODS.SECRETS_MANAGER:
            shouldDelete = isIamRef
            break
          case CONNECTION_METHODS.AWS_IAM:
            shouldDelete = !isIamRef
            break
          default:
            shouldDelete = false
        }
        if (shouldDelete) {
          stagedSecrets[key] = { action: 'delete' }
        }
      }
      return { stagedSecrets, renames: {}, fieldSources: {} }
    })
  },

  // Re-encodes every Secrets Manager reference (persisted + staged)
  // under the newly-picked provider's prefix. Without this, swapping
  // the top-level provider would only update the dropdown — values
  // keep their original prefix, save() emits an empty patch, and the
  // next load derives the old provider again. `_aws_iam_rds:` refs are
  // skipped (they belong to AWS IAM mode, not a provider choice).
  setSecretsManagerProvider: (newProvider) => {
    set((state) => {
      const persisted = state.connection?.secret || {}
      const stagedSecrets = { ...state.stagedSecrets }
      const fieldSources = { ...state.fieldSources }

      for (const [key, encoded] of Object.entries(persisted)) {
        if (!encoded || !isSecretReference(encoded)) continue
        if (decodeSecretValue(encoded).startsWith('_aws_iam_rds:')) continue
        stagedSecrets[key] = {
          action: 'replace',
          value: encodeSecretForSource(decodeForDisplay(encoded), newProvider),
        }
        fieldSources[key] = newProvider
      }

      for (const [key, change] of Object.entries(state.stagedSecrets)) {
        if (!change || change.action === 'delete' || !change.value) continue
        if (!isSecretReference(change.value)) continue
        if (decodeSecretValue(change.value).startsWith('_aws_iam_rds:')) continue
        stagedSecrets[key] = {
          ...change,
          value: encodeSecretForSource(decodeForDisplay(change.value), newProvider),
        }
        fieldSources[key] = newProvider
      }

      return { stagedSecrets, fieldSources }
    })
  },

  hasPendingChanges: () => {
    const state = get()
    if (Object.entries(state.stagedSecrets).some(([k, ch]) => !isPlaceholderEntry(k, ch))) return true
    if (Object.keys(state.renames).length > 0) return true
    return Object.keys(buildDraftsPatch(state.drafts, state.baseline)).length > 0
  },

  // Empty value = delete (per backend mergeSecrets). Renames become
  // delete-old + replace-new so row position is preserved in the UI
  // while the wire stays correct. SSH_PRIVATE_KEY gets a trailing
  // newline because the agent's parser requires it (mirrors CLJS
  // helpers.cljs config->json).
  buildSecretsPatch: () => {
    const { stagedSecrets, renames, connection } = get()
    const out = {}
    for (const [key, change] of Object.entries(stagedSecrets)) {
      if (isPlaceholderEntry(key, change)) continue
      if (change.action === 'delete') {
        out[key] = ''
      } else {
        out[key] = change.value
      }
    }
    for (const [origKey, newKey] of Object.entries(renames)) {
      if (origKey === newKey) continue
      const stagedValue = stagedSecrets[origKey]?.value
      const persisted = connection?.secret?.[origKey] || ''
      out[origKey] = ''
      out[newKey] = stagedValue || persisted
    }
    const sshKey = out['filesystem:SSH_PRIVATE_KEY']
    if (sshKey) {
      try {
        const decoded = atob(sshKey)
        if (decoded && !decoded.endsWith('\n')) {
          out['filesystem:SSH_PRIVATE_KEY'] = btoa(decoded + '\n')
        }
      } catch {
        // Malformed base64 — leave as-is; the backend will reject it.
      }
    }
    return out
  },

  save: async () => {
    const { connection, stagedSecrets, renames, drafts, baseline } = get()
    if (!connection) return
    // Reject placeholder rows that have a value but no name — saving
    // them as-is would persist sentinel keys like `envvar:NEW_KEY_1`.
    const unnamed = Object.entries(stagedSecrets).find(
      ([k, ch]) =>
        ch.action === 'new' &&
        ch.value &&
        PLACEHOLDER_KEY_RE.test(k) &&
        !renames[k],
    )
    if (unnamed) {
      const [k] = unnamed
      const kind = k.startsWith('envvar:')
        ? 'environment variable'
        : 'configuration file'
      const message = `Please name the new ${kind} before saving.`
      const err = new Error(message)
      err.message = message
      set({ error: message })
      throw err
    }
    set({ saving: true, error: null })
    try {
      const payload = buildDraftsPatch(drafts, baseline)
      if (Object.keys(stagedSecrets).length > 0) {
        payload.secret = get().buildSecretsPatch()
      }
      const updated = await connectionsService.patchConnection(connection.name, payload)
      const newDrafts = draftsFromConnection(updated)
      set({
        connection: updated,
        stagedSecrets: {},
        drafts: newDrafts,
        baseline: newDrafts,
        saving: false,
      })
      return updated
    } catch (err) {
      const message = err?.response?.data?.message || err?.message || 'Failed to save connection.'
      set({ error: message, saving: false })
      throw err
    }
  },

  deleteConnection: async () => {
    const { connection } = get()
    if (!connection) return
    set({ deleting: true, error: null })
    try {
      await connectionsService.deleteConnection(connection.name)
      set({ deleting: false })
    } catch (err) {
      const message = err?.response?.data?.message || 'Failed to delete connection.'
      set({ error: message, deleting: false })
      throw err
    }
  },

  runTestConnection: async () => {
    const { connection } = get()
    if (!connection) return
    const startedAt = Date.now()
    set({
      testing: true,
      testResult: null,
      testModalOpen: true,
      testAgentStatus: 'checking',
      testConnectionStatus: 'checking',
      testStartedAt: startedAt,
      testDurationMs: null,
    })

    // Mirrors the CLJS event handler in events/connections.cljs:244-319 —
    // two parallel GETs that update the modal as each one resolves so
    // the user sees each status flip independently. The duration shown
    // at the end is the wall-clock time until both finished.
    const settleAgent = (status) =>
      set((state) => ({
        testAgentStatus: status,
        ...maybeSettleDuration({
          startedAt,
          agentStatus: status,
          connectionStatus: state.testConnectionStatus,
        }),
      }))
    const settleConnection = (status, message) =>
      set((state) => ({
        testConnectionStatus: status,
        testResult:
          status === 'failed'
            ? { success: false, message }
            : status === 'successful'
              ? { success: true }
              : null,
        ...maybeSettleDuration({
          startedAt,
          agentStatus: state.testAgentStatus,
          connectionStatus: status,
        }),
      }))

    const agentReq = connectionsService
      .getConnection(connection.name)
      .then((data) => settleAgent(data?.status === 'online' ? 'online' : 'offline'))
      .catch(() => settleAgent('offline'))

    const connReq = connectionsService
      .testConnection(connection.name)
      .then(() => settleConnection('successful'))
      .catch((err) => {
        const message = err?.response?.data?.message || 'Connection test failed.'
        settleConnection('failed', message)
      })

    await Promise.all([agentReq, connReq])
    set({ testing: false })
  },

  closeTestModal: () =>
    set({
      testModalOpen: false,
      testResult: null,
      testAgentStatus: 'checking',
      testConnectionStatus: 'checking',
      testStartedAt: null,
      testDurationMs: null,
    }),

  clearTestResult: () => set({ testResult: null }),

  reset: () => set(initialState),
}))
