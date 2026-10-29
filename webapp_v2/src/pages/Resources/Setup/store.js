import { create } from 'zustand'
import { agentsService } from '@/services/agents'
import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { attributesService } from '@/services/attributes'
import { connectionsService } from '@/services/connections'
import { dataMaskingService } from '@/services/dataMasking'
import { guardrailsService } from '@/services/guardrails'
import { protectionProfilesService } from '@/services/protectionProfiles'
import { resourcesService } from '@/services/resources'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'
import { showSnackbar } from '@/utils/snackbar'
import { SOURCES } from '@/pages/Roles/Configure/utils/secretsCodec'
import { buildCreatePayload } from './payload'
import { createRoleCredentialsStore } from './roleCredentials'

export const STEPS = ['name', 'agent', 'roles', 'success']

function errorText(error) {
  return error?.response?.data?.message || error?.message || ''
}

function roleName(resourceName, suffix) {
  return `${resourceName}-role-${suffix}`
}

let lastRoleId = 0

// A response commits only while its wizard run and its request are still the
// latest of their kind. latest(kind) starts a request and answers its check.
let run = 0
const requests = {}
function latest(kind) {
  const id = (requests[kind] = (requests[kind] ?? 0) + 1)
  const started = run
  return () => started === run && requests[kind] === id
}

function newRole(setup, resourceName) {
  const suffix = String(Math.floor(1000 + Math.random() * 9000))
  lastRoleId += 1
  return {
    id: lastRoleId,
    suffix,
    name: roleName(resourceName, suffix),
    nameEdited: false,
    attributes: [],
    skipProfile: false,
    method: CONNECTION_METHODS.MANUAL,
    provider: SOURCES.VAULT_KV1,
    credentials: createRoleCredentialsStore(setup),
  }
}

// lookup: finding the new agent's id in GET /agents ('idle', 'pending', 'done', 'error').
const NEW_AGENT = { name: '', status: 'idle', token: null, lookup: 'idle' }

const initialState = {
  setup: null,
  step: 'name',
  name: '',
  checkingName: false,
  agents: [],
  agentsLoading: false,
  agentId: null,
  agentMode: 'select',
  newAgent: NEW_AGENT,
  installMethod: 'docker',
  roles: [],
  attributes: [],
  profile: null,
  // Save waits for 'ready': an unknown profile would drop its attribute.
  roleOptions: 'idle',
  submitting: false,
  createdRoles: [],
  activation: null,
}

// Every wizard run starts from start(setup): nothing carries over from an
// earlier run or from the CLJS wizard.
export const useResourceSetupStore = create((set, get) => {
  const updateRole = (id, patch) =>
    set((state) => ({
      roles: state.roles.map((role) => (role.id === id ? { ...role, ...patch } : role)),
    }))

  return {
    ...initialState,

    start: (setup) => {
      run += 1
      set({ ...initialState, setup })
    },

    setStep: (step) => set({ step }),

    // A role name the admin did not edit follows the resource name.
    setName: (value) => {
      const name = value.replace(/\s+/g, '-')
      set((state) => ({
        name,
        roles: state.roles.map((role) =>
          role.nameEdited ? role : { ...role, name: roleName(name, role.suffix) },
        ),
      }))
    },

    // GET /resources/:name answers 404 when the name is free.
    // An answer for a name the admin has since changed is dropped.
    checkName: async () => {
      const { name } = get()
      const request = latest('name')
      const current = () => request() && get().name === name
      set({ checkingName: true })
      try {
        await resourcesService.get(name)
        if (!current()) return
        showSnackbar({
          level: 'error',
          text: 'This resource name is already in use. Please choose a different name.',
        })
      } catch (error) {
        if (!current()) return
        if (error?.response?.status === 404) {
          set({ step: 'agent' })
        } else {
          showSnackbar({ level: 'error', text: 'Failed to validate resource name' })
        }
      } finally {
        set({ checkingName: false })
      }
    },

    loadAgents: async () => {
      const current = latest('agents')
      set({ agentsLoading: true })
      try {
        const { data } = await agentsService.list()
        if (current()) set({ agents: data ?? [], agentsLoading: false })
      } catch (error) {
        if (!current()) return
        set({ agentsLoading: false })
        showSnackbar({ level: 'error', text: 'Failed to fetch agents', description: errorText(error) })
      }
    },

    selectAgent: (agentId) => set({ agentId }),

    setAgentMode: (agentMode) => set({ agentMode }),

    setNewAgentName: (name) => set((state) => ({ newAgent: { ...state.newAgent, name } })),

    setInstallMethod: (installMethod) => set({ installMethod }),

    // Leaving the agent step or a new run drops a create still in flight.
    createAgent: async () => {
      const { name } = get().newAgent
      const current = latest('agent')
      set({ newAgent: { ...NEW_AGENT, name, status: 'creating' } })
      try {
        const { data } = await agentsService.create({ name })
        if (!current()) return
        set({ newAgent: { ...NEW_AGENT, name, status: 'ready', token: data?.token ?? null } })
      } catch (error) {
        if (!current()) return
        set({ newAgent: { ...NEW_AGENT, name, status: 'error' } })
        showSnackbar({ level: 'error', text: errorText(error) || 'Failed to create the agent' })
        return
      }
      await get().findNewAgent()
    },

    // POST /agents answers only the key, so the new agent's id comes from
    // GET /agents, by name. A failed lookup keeps the key on screen for a retry.
    findNewAgent: async () => {
      const { name } = get().newAgent
      const current = latest('lookup')
      const currentList = latest('agents')
      set((state) => ({ newAgent: { ...state.newAgent, lookup: 'pending' } }))
      let created
      try {
        const { data } = await agentsService.list()
        if (currentList()) set({ agents: data ?? [], agentsLoading: false })
        if (!current()) return
        created = (data ?? []).find((agent) => agent.name === name)
      } catch (error) {
        if (currentList()) set({ agentsLoading: false })
        if (!current()) return
        showSnackbar({ level: 'error', text: 'Failed to fetch agents', description: errorText(error) })
      }
      set((state) => ({
        agentId: created?.id ?? null,
        newAgent: { ...state.newAgent, lookup: created ? 'done' : 'error' },
      }))
    },

    // Back from the agent step forgets the agent, as CLJS clear-agent-state does.
    leaveAgentStep: () => {
      latest('agent')
      latest('lookup')
      set({ step: 'name', agentId: null, agentMode: 'select', newAgent: NEW_AGENT })
    },

    goToRoles: () =>
      set((state) => ({
        step: 'roles',
        roles: state.roles.length > 0 ? state.roles : [newRole(state.setup, state.name)],
      })),

    loadRoleOptions: async () => {
      const current = latest('roleOptions')
      set({ roleOptions: 'loading' })
      const [attributes, profile] = await Promise.allSettled([
        attributesService.listAll(),
        protectionProfilesService.get(),
      ])
      if (!current()) return
      set({
        attributes: attributes.status === 'fulfilled' ? attributes.value : [],
        profile: profile.status === 'fulfilled' ? profile.value : null,
        roleOptions: profile.status === 'fulfilled' ? 'ready' : 'error',
      })
    },

    addRole: () => set((state) => ({ roles: [...state.roles, newRole(state.setup, state.name)] })),

    removeRole: (id) => set((state) => ({ roles: state.roles.filter((role) => role.id !== id) })),

    setRoleName: (id, name) => updateRole(id, { name, nameEdited: true }),

    setRoleAttributes: (id, attributes, skipProfile) => updateRole(id, { attributes, skipProfile }),

    // The role gets the attribute at once, as in CLJS, so a save right after
    // still carries it. A failed create takes it back off.
    createAttribute: async (id, name) => {
      const withAttribute = (role) => (role.id === id ? { ...role, attributes: [...role.attributes, name] } : role)
      const withoutAttribute = (role) =>
        role.id === id ? { ...role, attributes: role.attributes.filter((a) => a !== name) } : role
      set((state) => ({
        attributes: [...state.attributes, { name }],
        roles: state.roles.map(withAttribute),
      }))
      try {
        await attributesService.create({ name })
      } catch (error) {
        set((state) => ({
          attributes: state.attributes.filter((a) => a.name !== name),
          roles: state.roles.map(withoutAttribute),
        }))
        showSnackbar({ level: 'error', text: 'Failed to create attribute', description: errorText(error) })
      }
    },

    setRoleMethod: (id, method) => {
      const role = get().roles.find((r) => r.id === id)
      role.credentials.getState().applyMethod(method, role.provider)
      updateRole(id, { method })
    },

    setRoleProvider: (id, provider) => {
      const role = get().roles.find((r) => r.id === id)
      role.credentials.getState().applyMethod(role.method, provider)
      updateRole(id, { provider })
    },

    submit: async (connections) => {
      const { name, setup, agentId, roles, profile, roleOptions } = get()
      if (roleOptions !== 'ready') return
      const payload = buildCreatePayload({
        name,
        setup,
        agentId,
        roles,
        connections,
        profileAttribute: profile?.attribute_name ?? null,
      })
      set({ submitting: true })
      try {
        await resourcesService.create(payload)
      } catch (error) {
        set({ submitting: false })
        showSnackbar({ level: 'error', text: 'Failed to create resource', description: errorText(error) })
        return
      }
      showSnackbar({ level: 'success', text: 'Resource created successfully!' })
      set({ submitting: false, createdRoles: payload.roles, step: 'success' })
    },

    // The activation cards: the org's rules per feature, whether the AI
    // provider is set up, and the ids of the created roles.
    loadActivation: async () => {
      const current = latest('activation')
      set({ activation: null })
      const names = get().createdRoles.map((role) => role.name)
      const [guardrails, masking, analyzer, provider, ...roles] = await Promise.allSettled([
        guardrailsService.list(),
        dataMaskingService.list(),
        aiSessionAnalyzerService.listRules(),
        aiSessionAnalyzerService.getProvider(),
        ...names.map((name) => connectionsService.list({ name, page: 1, page_size: 1 })),
      ])
      if (!current()) return
      const value = (result, pick) => (result.status === 'fulfilled' ? pick(result.value.data) ?? [] : [])
      roles.forEach((result, i) => {
        if (result.status === 'rejected') {
          showSnackbar({ level: 'error', text: `Failed to fetch role: ${names[i]}` })
        }
      })
      set({
        activation: {
          lists: {
            guardrails: value(guardrails, (data) => data),
            masking: value(masking, (data) => data),
            'ai-analyzer': value(analyzer, (data) => data?.data),
          },
          providerOk: provider.status === 'fulfilled',
          roles: roles
            .filter((result) => result.status === 'fulfilled')
            .map((result) => result.value.data?.data?.[0])
            .filter((conn) => conn?.id)
            .map((conn) => ({ id: conn.id, name: conn.name })),
        },
      })
    },
  }
})
