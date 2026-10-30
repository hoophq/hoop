import { create } from 'zustand'
import { awsConnectService } from '@/services/awsConnect'
import { useAuthStore } from '@/stores/useAuthStore'
import { showSnackbar } from '@/utils/snackbar'
import { errorMessage } from '../utils'
import {
  AUTH_CREDENTIALS,
  buildResourceTree,
  credentialsBody,
  jobPayload,
  resourceIds,
  roleName,
  selectedTree,
} from './wizard'

const initial = {
  step: 'credentials',
  // The loading message while a request runs, else null.
  busy: null,
  authMethod: AUTH_CREDENTIALS,
  credentials: { accessKeyId: '', secretAccessKey: '', sessionToken: '', region: null },
  credentialsError: null,
  accounts: [],
  selectedAccounts: [],
  skipConnected: true,
  accountsError: null,
  tree: [],
  connected: [],
  selectedResources: [],
  securityGroups: {},
  roleNames: {},
  agentAssignments: {},
  createConnection: true,
  vaultEnabled: false,
  vaultPath: '',
  // The new root passwords; the modal is open while they are set.
  rootPasswords: null,
  jobs: {},
}

// A response from before reset() belongs to a wizard that is gone.
let session = 0

export const useAwsConnectSetupStore = create((set, get) => {
  const current = () => {
    const id = session
    return () => id === session
  }

  return {
    ...initial,

    update: (patch) => set(patch),
    setCredential: (field, value) => set((s) => ({ credentials: { ...s.credentials, [field]: value } })),
    setMapValue: (map, id, value) => set((s) => ({ [map]: { ...s[map], [id]: value } })),

    saveCredentials: async () => {
      const live = current()
      const { authMethod, credentials } = get()
      set({ busy: 'Saving AWS credentials...', credentialsError: null })
      try {
        await awsConnectService.saveCredentials(credentialsBody(authMethod, credentials))
      } catch (err) {
        if (!live()) return
        const message = errorMessage(err, 'Failed to save AWS credentials')
        set({ busy: null, credentialsError: message })
        showSnackbar({ level: 'error', text: 'Failed to save AWS credentials', description: message })
        return
      }
      if (!live()) return
      set({ busy: 'Retrieving AWS organization accounts...' })
      try {
        const { data } = await awsConnectService.listAccounts()
        if (!live()) return
        set({ busy: null, accounts: data?.items ?? [], step: 'accounts' })
      } catch (err) {
        if (!live()) return
        set({ busy: null, credentialsError: errorMessage(err, 'Failed to fetch AWS organization accounts') })
      }
    },

    describeInstances: async () => {
      const live = current()
      const { selectedAccounts, accounts, skipConnected } = get()
      set({ busy: 'Retrieving AWS resources in your environment...', accountsError: null })
      try {
        const { data } = await awsConnectService.describeDbInstances(selectedAccounts)
        if (!live()) return
        const { tree, connected } = buildResourceTree(data?.items ?? [], accounts, skipConnected)
        const ids = resourceIds(tree)
        set((s) => ({
          busy: null,
          tree,
          connected,
          selectedResources: s.selectedResources.filter((id) => ids.includes(id)),
          step: 'resources',
        }))
      } catch (err) {
        if (!live()) return
        const message = errorMessage(err, 'Failed to retrieve AWS database instances')
        set({ busy: null, accountsError: message })
        showSnackbar({ level: 'error', text: 'Failed to retrieve AWS database instances', description: message })
      }
    },

    resetRootPasswords: async () => {
      const live = current()
      set({ busy: 'Resetting root passwords...' })
      try {
        const { data } = await awsConnectService.resetRootPasswords(get().selectedResources)
        if (!live()) return
        set({ busy: null, rootPasswords: data })
      } catch (err) {
        if (!live()) return
        set({ busy: null })
        showSnackbar({
          level: 'error',
          text: 'Failed to reset root passwords',
          description: errorMessage(err, 'The gateway did not reset the passwords.'),
        })
      }
    },

    // A 202 means the job started, not that it is done (Q34).
    startJobs: async () => {
      const live = current()
      const state = get()
      const resources = selectedTree(state.tree, state.selectedResources).flatMap((account) => account.children)
      const jobs = Object.fromEntries(
        resources.map((resource) => [
          resource.id,
          { status: 'pending', name: roleName(resource, state.roleNames), resource, error: null },
        ]),
      )
      set({ rootPasswords: null, step: 'status', jobs })

      const finish = (id, patch) => {
        if (live()) set((s) => ({ jobs: { ...s.jobs, [id]: { ...s.jobs[id], ...patch } } }))
      }
      await Promise.all(
        resources.map(async (resource) => {
          const name = jobs[resource.id].name
          try {
            await awsConnectService.createJob(jobPayload(resource, state))
            finish(resource.id, { status: 'success' })
            showSnackbar({ level: 'success', text: `Job started for ${name}.` })
          } catch (err) {
            const message = errorMessage(err, 'Failed to start the job')
            finish(resource.id, { status: 'failure', error: message })
            showSnackbar({ level: 'error', text: `Failed to start the job for ${name}`, description: message })
          }
        }),
      )
    },

    reset: () => {
      session++
      set(initial)
    },
  }
})

useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) useAwsConnectSetupStore.getState().reset()
})
