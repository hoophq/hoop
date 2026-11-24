import { create } from 'zustand'
import { agentsService } from '@/services/agents'
import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { attributesService } from '@/services/attributes'
import { connectionsService } from '@/services/connections'
import { dataMaskingService } from '@/services/dataMasking'
import { federationService } from '@/services/federation'
import { guardrailsService } from '@/services/guardrails'
import { protectionProfilesService } from '@/services/protectionProfiles'
import { resourcesService } from '@/services/resources'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'
import { showSnackbar } from '@/utils/snackbar'
import { SOURCES } from '@/pages/Roles/Configure/utils/secretsCodec'
import { buildFederationConfig } from '@/pages/Roles/Configure/utils/federation'
import { setupConfigForResource } from './catalog'
import { buildCreatePayload } from './payload'
import { createRoleCredentialsStore } from './roleCredentials'

export const STEPS = ['name', 'agent', 'roles', 'success']

function errorText(error) {
  return error?.response?.data?.message || error?.message || ''
}

// One PUT /connections/:role/federation per federation role, all in parallel.
// Answers the roles whose PUT failed, with the first error.
async function saveFederations(roles) {
  const federated = roles.filter((role) => role.method === CONNECTION_METHODS.IAM_FEDERATION)
  const results = await Promise.allSettled(
    federated.map((role) =>
      federationService.save(role.name, buildFederationConfig(role.credentials.getState().federation.form)),
    ),
  )
  const failed = federated.filter((_, i) => results[i].status === 'rejected').map((role) => role.name)
  const error = results.find((result) => result.status === 'rejected')?.reason
  return { failed, error }
}

// The gateway refuses to delete a resource that still has roles, so every
// role goes first. Answers what could not be deleted.
async function rollBack(resourceName, roleNames) {
  const results = await Promise.allSettled(roleNames.map((name) => connectionsService.deleteConnection(name)))
  const left = roleNames.filter((_, i) => results[i].status === 'rejected')
  if (left.length > 0) return [...left, resourceName]
  try {
    await resourcesService.remove(resourceName)
    return []
  } catch {
    return [resourceName]
  }
}

// Add role: POST /connections, then the federation config. A role whose
// federation save fails is deleted again, so the admin can add it later.
async function createRole(role, body) {
  let created
  try {
    created = await connectionsService.create(body)
  } catch (error) {
    return { name: body.name, error: errorText(error) }
  }
  if (role.method !== CONNECTION_METHODS.IAM_FEDERATION) return { name: body.name, created }
  try {
    await federationService.save(body.name, buildFederationConfig(role.credentials.getState().federation.form))
    return { name: body.name, created }
  } catch (error) {
    const reason = `IAM Federation setup failed: ${errorText(error)}`
    try {
      await connectionsService.deleteConnection(body.name)
      return { name: body.name, error: reason }
    } catch {
      return { name: body.name, error: `${reason} Could not delete the role: delete it before you try again.` }
    }
  }
}

function roleName(resourceName, suffix) {
  return `${resourceName}-role-${suffix}`
}

let lastRoleId = 0

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
  // Save waits for 'ready': the attributes and the protection profile loaded.
  roleOptions: 'idle',
  submitting: false,
  createdRoles: [],
  // Add role: the roles that failed, each { name, error }.
  failedRoles: [],
  // Add role: the resource the run adds roles to.
  resource: null,
  activation: null,
}

const NO_TARGET = { name: null, status: 'idle', resource: null, error: null }

// Every wizard run starts from start(setup): nothing carries over from an
// earlier run or from the CLJS wizard.
export const useResourceSetupStore = create((set, get) => {
  const updateRole = (id, patch) =>
    set((state) => ({
      roles: state.roles.map((role) => (role.id === id ? { ...role, ...patch } : role)),
    }))

  // A response commits only while its wizard run and its request are still the
  // latest of their kind. latest(kind) starts a request and answers its check.
  const latest = (kind) => {
    const { run, requests } = get()
    const id = (requests[kind] ?? 0) + 1
    set({ requests: { ...requests, [kind]: id } })
    return () => get().run === run && get().requests[kind] === id
  }

  return {
    ...initialState,
    run: 0,
    requests: {},
    // Add role: GET /resources/:name, read by the route switch and the page.
    // status: 'idle', 'loading', 'ready', 'not-found', 'error'.
    target: NO_TARGET,

    start: (setup) => set((state) => ({ ...initialState, setup, run: state.run + 1 })),

    loadTarget: async (name) => {
      const current = latest('target')
      set({ target: { ...NO_TARGET, name, status: 'loading' } })
      try {
        const { data } = await resourcesService.get(name)
        if (current()) set({ target: { ...NO_TARGET, name, status: 'ready', resource: data } })
      } catch (error) {
        if (!current()) return
        const status = error?.response?.status === 404 ? 'not-found' : 'error'
        set({ target: { ...NO_TARGET, name, status, error: errorText(error) } })
      }
    },

    clearTarget: () => {
      latest('target')
      set({ target: NO_TARGET })
    },

    // The roles step of an existing resource: its name and agent, one role.
    startAddRole: (resource) => {
      const setup = setupConfigForResource(resource)
      set((state) => ({
        ...initialState,
        setup,
        resource,
        name: resource.name,
        agentId: resource.agent_id,
        step: 'roles',
        roles: [newRole(setup, resource.name)],
        run: state.run + 1,
      }))
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

    // A new agent replaces the selected one: Next waits for the new agent's id.
    setAgentMode: (agentMode) => set({ agentMode, agentId: null }),

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
      const loaded = attributes.status === 'fulfilled' && profile.status === 'fulfilled'
      set({
        attributes: attributes.status === 'fulfilled' ? attributes.value : [],
        profile: profile.status === 'fulfilled' ? profile.value : null,
        roleOptions: loaded ? 'ready' : 'error',
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
      const current = latest('submit')
      set({ submitting: true })
      let warned = []
      try {
        const response = await resourcesService.create(payload)
        warned = response?.data?.mcp_oauth_warnings ?? []
      } catch (error) {
        if (!current()) return
        set({ submitting: false })
        showSnackbar({ level: 'error', text: 'Failed to create resource', description: errorText(error) })
        return
      }
      // The resource exists now: a failed federation save rolls it back, even
      // after the admin left the wizard, so a retry starts clean.
      const federation = await saveFederations(roles)
      if (federation.failed.length > 0) {
        const left = await rollBack(name, payload.roles.map((role) => role.name))
        if (!current()) return
        set({ submitting: false })
        showSnackbar({
          level: 'error',
          text:
            left.length > 0
              ? `IAM Federation setup failed for ${federation.failed.join(', ')}. Could not delete ${left.join(', ')}: delete them before you try again.`
              : `IAM Federation setup failed for ${federation.failed.join(', ')}. The resource was not created.`,
          description: errorText(federation.error),
        })
        return
      }
      if (!current()) return
      showSnackbar({ level: 'success', text: 'Resource created successfully!' })
      if (warned.length > 0) {
        showSnackbar({
          level: 'warning',
          text: `MCP OAuth login not attached to ${warned.map((w) => w.name).join(', ')}`,
          description: warned[0].warning,
        })
      }
      set({ submitting: false, createdRoles: payload.roles, step: 'success' })
    },

    // Add role: one POST /connections per role, in parallel, each with the
    // body POST /resources would carry. The success step shows only when a
    // role was created, and names the ones that failed.
    addRoles: async (connections) => {
      const { name, setup, agentId, roles, profile, roleOptions } = get()
      if (roleOptions !== 'ready') return
      const bodies = buildCreatePayload({
        name,
        setup,
        agentId,
        roles,
        connections,
        profileAttribute: profile?.attribute_name ?? null,
      }).roles.map((body) => ({ ...body, resource_name: name }))
      const current = latest('submit')
      set({ submitting: true })
      const results = await Promise.all(bodies.map((body, i) => createRole(roles[i], body)))
      if (!current()) return
      const created = results.filter((result) => result.created)
      const failed = results.filter((result) => result.error).map(({ name, error }) => ({ name, error }))
      failed.forEach((role) =>
        showSnackbar({ level: 'error', text: `Failed to create role: ${role.name}`, description: role.error }),
      )
      showSnackbar({
        level: failed.length > 0 ? 'warning' : 'success',
        text: `${created.length} role(s) created${failed.length > 0 ? `, ${failed.length} failed` : ''}`,
      })
      const warned = created.filter((result) => result.created?.mcp_oauth_warning)
      if (warned.length > 0) {
        showSnackbar({
          level: 'warning',
          text: `MCP OAuth login not attached to ${warned.map((result) => result.name).join(', ')}`,
          description: warned[0].created.mcp_oauth_warning,
        })
      }
      if (created.length === 0) {
        set({ submitting: false })
        return
      }
      const createdNames = new Set(created.map((result) => result.name))
      set({
        submitting: false,
        createdRoles: bodies.filter((body) => createdNames.has(body.name)),
        failedRoles: failed,
        step: 'success',
      })
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
