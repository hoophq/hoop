import { create } from 'zustand'
import { connectionsService } from '@/services/connections'
import { jiraTemplatesService } from '@/services/jiraTemplates'
import { useAuthStore } from '@/stores/useAuthStore'
import { PENDING, allDependents, filterFor, indexConfigs } from '@/features/JiraPromptGate/cascade'
import {
  initFields,
  missingRequired,
  needsForm,
  selectCmdb,
  submitFields,
} from '@/features/JiraPromptGate/jiraFields'

/**
 * The Jira prompt gate: the React port of the CLJS runtime prompt
 * (webapp/src/webapp/events/jira_templates.cljs, jira_templates/*).
 *
 * A caller asks `load(connectionName)` before it runs something on a resource.
 * The answer says whether the user must fill the template's prompts. When not,
 * `fields` is what to add to the request; when so, the caller opens
 * <JiraPromptGate>, which hands the fields to its onSubmit.
 *
 * Departures from CLJS, each a bug there:
 * - The decision waits for the CMDB values, so a Re-run on a template with
 *   CMDB rows continues (CLJS left its loading modal open).
 * - "Try again" after a CMDB error stays in the caller's flow (CLJS went on
 *   into the Runbooks flow) and keeps what the user typed.
 * - CMDB presets the values hold are sent when no form is needed (CLJS sent
 *   nothing). A row without one opens the form.
 * - The integration status is read here, so there is no race with its fetch.
 */

export const GATE_STATUS = {
  IDLE: 'idle',
  LOADING: 'loading',
  READY: 'ready',
  ERROR: 'error',
}

const PAGE_SIZE = 50
const SEARCH_DEBOUNCE_MS = 300

const initial = {
  status: GATE_STATUS.IDLE,
  prompts: [],
  // The template's CMDB items, plus the options loaded for each one.
  rows: [],
  fieldconfigs: {},
  // The form's jira_fields (see jiraFields.js).
  fields: {},
  // Required fields left empty at the last submit.
  missing: [],
}

// One generation per load(): a response of an older load changes nothing.
let generation = 0
// Per row (jira_field), the latest fetch: only it may change the row's options,
// so a slow unfiltered response cannot replace AQL-filtered ones.
const rowRequestIds = {}
const searchTimers = {}

const isBlank = (s) => s == null || String(s).trim() === ''

const toRow = (item) => ({
  ...item,
  selectedName: null,
  jira_values: [],
  hasMore: false,
  offset: 0,
  search: '',
  loading: false,
  failed: false,
})

function clearSearchTimers() {
  for (const field of Object.keys(searchTimers)) {
    clearTimeout(searchTimers[field])
    delete searchTimers[field]
  }
}

export const useJiraPromptStore = create((set, get) => {
  const patchRow = (jiraField, patch) =>
    set((state) => ({
      rows: state.rows.map((r) =>
        r.jira_field === jiraField ? { ...r, ...(typeof patch === 'function' ? patch(r) : patch) } : r,
      ),
    }))

  // Fetches one page of a row's options with its cascade filter. Resolves to
  // 'ok', 'failed' or 'stale'. A failed cascade refetch keeps what the row
  // shows (its dependents were emptied when the pick changed) and is not an
  // error: an error page would hide what the user already filled.
  const fetchRow = async (jiraField, { offset = 0, cascade = false } = {}) => {
    const gen = generation
    const { rows, fieldconfigs } = get()
    const row = rows.find((r) => r.jira_field === jiraField)
    if (!row) return 'stale'
    const requestId = (rowRequestIds[jiraField] ?? 0) + 1
    rowRequestIds[jiraField] = requestId
    const isCurrent = () => gen === generation && rowRequestIds[jiraField] === requestId

    const filter = filterFor(row, rows, fieldconfigs)
    if (filter === PENDING) {
      patchRow(jiraField, { loading: false, failed: false, jira_values: [], hasMore: false, offset: 0 })
      return 'ok'
    }
    patchRow(jiraField, { loading: true })
    try {
      const { data } = await jiraTemplatesService.searchAssetObjects({
        objectTypeId: row.jira_object_type,
        // The field configuration owns the scope when present.
        objectSchemaId: filter?.objectSchemaId ?? row.jira_object_schema_id,
        name: row.search,
        aql: filter?.aql,
        limit: PAGE_SIZE,
        offset,
      })
      if (!isCurrent()) return 'stale'
      const values = data?.values ?? []
      patchRow(jiraField, (r) => ({
        loading: false,
        failed: false,
        jira_values: offset > 0 ? [...r.jira_values, ...values] : values,
        hasMore: Boolean(data?.has_next_page),
        offset,
      }))
      return 'ok'
    } catch {
      if (!isCurrent()) return 'stale'
      patchRow(jiraField, cascade ? { loading: false } : { loading: false, failed: true })
      return cascade ? 'ok' : 'failed'
    }
  }

  // Every row's first page. True when none failed.
  const fetchAllRows = async () => {
    const results = await Promise.all(get().rows.map((r) => fetchRow(r.jira_field)))
    return !results.includes('failed')
  }

  // What the caller does next: open the gate, or send `fields` at once.
  const decision = () => {
    const { prompts, rows, fields } = get()
    if (needsForm(prompts, rows, fields)) return { needed: true }
    return { needed: false, fields: rows.length ? submitFields(prompts, fields) : {} }
  }

  return {
    ...initial,

    /**
     * Loads the Jira template of a resource role and its CMDB options.
     * Resolves to:
     *   { needed: false, fields } - run now, with `fields` in the request body;
     *   { needed: true }          - open <JiraPromptGate> first;
     *   null                      - a newer load() replaced this one: do nothing.
     * Rejects when the resource, the integration or the template cannot be read.
     */
    load: async (connectionName) => {
      const gen = ++generation
      clearSearchTimers()
      set({ ...initial, status: GATE_STATUS.LOADING })
      try {
        const { data: connection } = await connectionsService.getConnection(connectionName)
        const templateId = connection?.jira_issue_template_id
        if (gen !== generation) return null
        if (isBlank(templateId)) {
          set({ status: GATE_STATUS.IDLE })
          return { needed: false, fields: {} }
        }

        const { data: integration } = await jiraTemplatesService.getIntegration()
        if (gen !== generation) return null
        if (integration?.status !== 'enabled') {
          set({ status: GATE_STATUS.IDLE })
          return { needed: false, fields: {} }
        }

        const { data: template } = await jiraTemplatesService.get(templateId)
        if (gen !== generation) return null
        const prompts = template?.prompt_types?.items ?? []
        const rows = (template?.cmdb_types?.items ?? []).map(toRow)

        let fieldconfigs = {}
        if (rows.length) {
          try {
            const jiraFields = [...new Set(rows.map((r) => r.jira_field).filter(Boolean))]
            const { data } = await jiraTemplatesService.getAssetFieldConfigs(jiraFields)
            fieldconfigs = indexConfigs(data?.items)
          } catch {
            // No cascade is better than no prompt: unfiltered dropdowns.
            fieldconfigs = {}
          }
          if (gen !== generation) return null
        }
        set({ prompts, rows, fieldconfigs })

        const ok = await fetchAllRows()
        if (gen !== generation) return null
        set({ fields: initFields(get().rows) })
        if (!ok) {
          set({ status: GATE_STATUS.ERROR })
          return { needed: true }
        }
        set({ status: GATE_STATUS.READY })
        return decision()
      } catch (err) {
        if (gen === generation) set({ ...initial })
        throw err
      }
    },

    // "Try again" on the CMDB error: every row's first page again. The answers
    // and picks stay. Resolves like load(), without the null.
    retry: async () => {
      const gen = generation
      clearSearchTimers()
      set((state) => ({
        status: GATE_STATUS.LOADING,
        rows: state.rows.map((r) => ({ ...r, search: '' })),
      }))
      const ok = await fetchAllRows()
      if (gen !== generation) return null
      set((state) => ({
        status: ok ? GATE_STATUS.READY : GATE_STATUS.ERROR,
        fields: { ...initFields(state.rows), ...state.fields },
      }))
      return ok ? decision() : { needed: true }
    },

    setAnswer: (jiraField, value) =>
      set((state) => ({
        fields: { ...state.fields, [jiraField]: value },
        missing: state.missing.filter((f) => f !== jiraField),
      })),

    // A CMDB pick: { value: objectId, label: name }. The rows filtered by it
    // lose their pick and options and fetch again.
    pickCmdb: (jiraField, pick) => {
      const { rows, fieldconfigs, fields, missing } = get()
      const row = rows.find((r) => r.jira_field === jiraField)
      if (!row) return
      const dependents = new Set(allDependents(row, rows, fieldconfigs).map((d) => d.jira_field))
      set({
        fields: selectCmdb(fields, row, rows, fieldconfigs, pick),
        rows: rows.map((r) => {
          if (r.jira_field === jiraField) return { ...r, value: pick.value, selectedName: pick.label }
          if (!dependents.has(r.jira_field)) return r
          return { ...r, value: null, selectedName: null, jira_values: [], hasMore: false, offset: 0, search: '' }
        }),
        missing: missing.filter((f) => f !== jiraField),
      })
      for (const field of dependents) {
        clearTimeout(searchTimers[field])
        delete searchTimers[field]
        fetchRow(field, { cascade: true })
      }
    },

    // Server search by object name, 300 ms after the last key.
    setSearch: (jiraField, term) => {
      patchRow(jiraField, { search: term })
      clearTimeout(searchTimers[jiraField])
      searchTimers[jiraField] = setTimeout(async () => {
        delete searchTimers[jiraField]
        if ((await fetchRow(jiraField)) === 'failed') set({ status: GATE_STATUS.ERROR })
      }, SEARCH_DEBOUNCE_MS)
    },

    loadMore: async (jiraField) => {
      const row = get().rows.find((r) => r.jira_field === jiraField)
      if (!row || row.loading || !row.hasMore) return
      if ((await fetchRow(jiraField, { offset: row.offset + PAGE_SIZE })) === 'failed') {
        set({ status: GATE_STATUS.ERROR })
      }
    },

    // Send: the request body part, or null when a required field is empty
    // (the store lists them in `missing`).
    submit: () => {
      const { prompts, rows, fields } = get()
      const missing = missingRequired(prompts, rows, fields)
      set({ missing })
      return missing.length ? null : submitFields(prompts, fields)
    },

    reset: () => {
      generation += 1
      clearSearchTimers()
      set({ ...initial })
    },
  }
})

useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) useJiraPromptStore.getState().reset()
})
