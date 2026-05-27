import { create } from 'zustand'
import { connectionsService } from '@/services/connections'
import { guardrailsService } from '@/services/guardrails'
import { jiraTemplatesService } from '@/services/jiraTemplates'
import { attributesService } from '@/services/attributes'
import { connectionTagsService } from '@/services/connectionTags'
import { userGroupsService } from '@/services/userGroups'
import { sourceFromEncodedValue } from './utils/secretsCodec'

// Local store for the Configure Role page.
//
// Lives next to the page (not in /stores/) because nothing outside this
// route consumes it. Mirrors the shape of the CLJS :connection-setup
// re-frame slice but only models the pieces this React page touches.
//
// Form drafts:
//   `drafts` mirrors every editable scalar/array field from the
//   connection payload. We seed it on load and PATCH on save with only
//   the keys that diverge from the loaded connection.
//
// Staged-secrets contract:
//   stagedSecrets[key] = { action: 'replace' | 'delete' | 'new', value?: string }
//   - 'replace' overrides an existing inline secret (value is base64-encoded).
//   - 'delete'  removes an existing key (custom connection type only).
//   - 'new'     adds a brand-new custom-type secret (key + value).

const emptyDrafts = {
  attributes: [],
  connection_tags: {},
  access_mode_exec: 'enabled',
  access_mode_runbooks: 'enabled',
  access_mode_connect: 'enabled',
  access_schema: 'enabled',
  guardrail_rules: [],
  jira_issue_template_id: '',
  mandatory_metadata_fields: [],
  redact_enabled: false,
  redact_types: [],
  // Review backward-compat (renders only when the connection already
  // has any of these set on load).
  reviewers: [],
  min_review_approvals: null,
  force_approve_groups: [],
  access_max_duration: null,
  // Connection-level fields editable from the credentials tab.
  agent_id: '',
  command: [],
}

function draftsFromConnection(conn) {
  if (!conn) return { ...emptyDrafts }
  return {
    attributes: conn.attributes || [],
    connection_tags: conn.connection_tags || {},
    access_mode_exec: conn.access_mode_exec || 'enabled',
    access_mode_runbooks: conn.access_mode_runbooks || 'enabled',
    access_mode_connect: conn.access_mode_connect || 'enabled',
    access_schema: conn.access_schema || 'enabled',
    guardrail_rules: conn.guardrail_rules || [],
    jira_issue_template_id: conn.jira_issue_template_id || '',
    mandatory_metadata_fields: conn.mandatory_metadata_fields || [],
    redact_enabled: !!conn.redact_enabled,
    redact_types: conn.redact_types || [],
    reviewers: conn.reviewers || [],
    min_review_approvals: conn.min_review_approvals ?? null,
    force_approve_groups: conn.force_approve_groups || [],
    access_max_duration: conn.access_max_duration ?? null,
    agent_id: conn.agent_id || '',
    command: conn.command || [],
  }
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

// Returns only the keys of `drafts` that differ from the connection's
// current value, formatted in the shape PATCH /connections expects.
function buildDraftsPatch(drafts, baseline) {
  const patch = {}
  if (!arraysEqual(drafts.attributes, baseline.attributes)) {
    patch.attributes = drafts.attributes
  }
  if (!objectsEqual(drafts.connection_tags, baseline.connection_tags)) {
    patch.connection_tags = drafts.connection_tags
  }
  if (drafts.access_mode_exec !== baseline.access_mode_exec) {
    patch.access_mode_exec = drafts.access_mode_exec
  }
  if (drafts.access_mode_runbooks !== baseline.access_mode_runbooks) {
    patch.access_mode_runbooks = drafts.access_mode_runbooks
  }
  if (drafts.access_mode_connect !== baseline.access_mode_connect) {
    patch.access_mode_connect = drafts.access_mode_connect
  }
  if (drafts.access_schema !== baseline.access_schema) {
    patch.access_schema = drafts.access_schema
  }
  if (!arraysEqual(drafts.guardrail_rules, baseline.guardrail_rules)) {
    patch.guardrail_rules = drafts.guardrail_rules
  }
  if (drafts.jira_issue_template_id !== baseline.jira_issue_template_id) {
    patch.jira_issue_template_id = drafts.jira_issue_template_id
  }
  if (!arraysEqual(drafts.mandatory_metadata_fields, baseline.mandatory_metadata_fields)) {
    patch.mandatory_metadata_fields = drafts.mandatory_metadata_fields
  }
  // redact_enabled is read-only on the API (derived from redact_types).
  // We translate by setting redact_types to [] when the user disables masking.
  const desiredRedactTypes = drafts.redact_enabled ? drafts.redact_types : []
  const baselineRedactTypes = baseline.redact_enabled ? baseline.redact_types : []
  if (!arraysEqual(desiredRedactTypes, baselineRedactTypes)) {
    patch.redact_types = desiredRedactTypes
  }
  if (!arraysEqual(drafts.reviewers, baseline.reviewers)) {
    patch.reviewers = drafts.reviewers
  }
  if (drafts.min_review_approvals !== baseline.min_review_approvals) {
    patch.min_review_approvals = drafts.min_review_approvals
  }
  if (!arraysEqual(drafts.force_approve_groups, baseline.force_approve_groups)) {
    patch.force_approve_groups = drafts.force_approve_groups
  }
  if (drafts.access_max_duration !== baseline.access_max_duration) {
    patch.access_max_duration = drafts.access_max_duration
  }
  if (drafts.agent_id !== baseline.agent_id) {
    patch.agent_id = drafts.agent_id
  }
  if (!arraysEqual(drafts.command, baseline.command)) {
    patch.command = drafts.command
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
  drafts: { ...emptyDrafts },
  baseline: { ...emptyDrafts }, // snapshot of drafts as loaded; used for diffing
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
          source === 'manual'
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
      return { stagedSecrets: next }
    })
  },

  hasPendingChanges: () => {
    const state = get()
    if (Object.keys(state.stagedSecrets).length > 0) return true
    return Object.keys(buildDraftsPatch(state.drafts, state.baseline)).length > 0
  },

  // Builds the secrets sub-payload for the PATCH request.
  // Empty value = delete (per backend mergeSecrets semantics).
  buildSecretsPatch: () => {
    const staged = get().stagedSecrets
    const out = {}
    for (const [key, change] of Object.entries(staged)) {
      if (change.action === 'delete') {
        out[key] = ''
      } else {
        out[key] = change.value
      }
    }
    return out
  },

  save: async () => {
    const { connection, stagedSecrets, drafts, baseline } = get()
    if (!connection) return
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
      const message = err?.response?.data?.message || 'Failed to save connection.'
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
