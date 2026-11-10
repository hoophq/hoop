import { federationService } from '@/services/federation'
import {
  adminCredentialsForTest,
  buildFederationTestPayload,
  DEFAULT_FEDERATION_FORM,
  FEDERATION_PROVIDERS,
} from '@/pages/Roles/Configure/utils/federation'

// `selected` is the IAM Federation card; `config` is what the gateway stores.
export const INITIAL_FEDERATION = {
  status: 'idle',
  config: null,
  form: DEFAULT_FEDERATION_FORM,
  baselineForm: DEFAULT_FEDERATION_FORM,
  selected: false,
  credentialsEditing: false,
  error: null,
  oauthStatus: null,
  oauthStatusError: null,
}

export const INITIAL_FEDERATION_TEST = { status: 'idle', result: null }

// The form actions FederationSection calls. Spread into the Configure Role
// store and into each role store of the resource wizard; both keep
// `federation`, `federationTest`, `connection` and `hideRoleInfo`.
export function federationSlice(set, get) {
  const setFederationForm = (patch) =>
    set((state) => ({
      federation: { ...state.federation, form: { ...state.federation.form, ...patch } },
    }))

  const setFederationCredentialsEditing = (credentialsEditing) =>
    set((state) => ({ federation: { ...state.federation, credentialsEditing } }))

  return {
    selectFederation: (selected) =>
      set((state) => ({ federation: { ...state.federation, selected } })),

    setFederationForm,

    // A credential typed for one provider is the wrong kind for the other.
    // gcp_oauth has no static service account to fall back on.
    setFederationProvider: (provider) => {
      setFederationCredentialsEditing(false)
      setFederationForm(
        provider === FEDERATION_PROVIDERS.GCP_OAUTH
          ? { builtin_provider: provider, admin_credentials_json: '', fallback_policy: 'deny' }
          : { builtin_provider: provider, admin_credentials_json: '' },
      )
    },

    setFederationProjectId: (projectId) =>
      set((state) => {
        const form = state.federation.form
        return {
          federation: {
            ...state.federation,
            form: { ...form, extra_config: { ...form.extra_config, project_id: projectId } },
          },
        }
      }),

    setFederationCredentialsEditing,

    runFederationTest: async (email, agentId) => {
      const { connection, federation, hideRoleInfo } = get()
      set({ federationTest: { status: 'loading', result: null } })
      try {
        const { data } = await federationService.test(
          buildFederationTestPayload({
            email,
            form: federation.form,
            agentId,
            connection,
            adminCredentials: adminCredentialsForTest(federation.form, connection.secret, hideRoleInfo),
          }),
        )
        set({ federationTest: { status: data?.success ? 'success' : 'error', result: data } })
      } catch (err) {
        const message = err?.response?.data?.message || err?.message || 'Federation test failed.'
        set({ federationTest: { status: 'error', result: { error: message } } })
      }
    },

    resetFederationTest: () => set({ federationTest: INITIAL_FEDERATION_TEST }),
  }
}
