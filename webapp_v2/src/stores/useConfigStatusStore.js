import { create } from 'zustand'
import { agentsService } from '@/services/agents'
import { connectionsService } from '@/services/connections'
import { sessionsService } from '@/services/sessions'
import { userGroupsService } from '@/services/userGroups'
import { usersService } from '@/services/users'
import { guardrailsService } from '@/services/guardrails'
import { dataMaskingService } from '@/services/dataMasking'
import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { useUserStore } from '@/stores/useUserStore'

// EVL-98: backs the sidebar Config Status checklist. Every check derives
// from real configuration state — nothing is persisted or manually ticked —
// so the widget updates automatically as the admin configures the org.

// Short TTL: navigation/focus/poll triggers all funnel through fetchStatus,
// and the TTL is what dedupes them — 15s keeps the widget feeling live
// without letting overlapping triggers stack requests.
const TTL_MS = 15_000

// The gateway's built-in admin group (GroupAdmin). Server-side it is
// env-overridable, but 'admin' is the default every org starts with, and the
// checklist only needs a fresh-org approximation.
const DEFAULT_GROUP = 'admin'

const INITIAL_CHECKS = {
  agentDeployed: false,
  resourceCreated: false,
  sessionRan: false,
  groupsCreated: false,
  peopleAssigned: false,
  guardrailsExplored: false,
  dataMaskingExplored: false,
  aiAnalyzerEnabled: false,
  protectionLevelSet: false,
}

const INITIAL_STATE = {
  status: 'idle', // 'idle' | 'loading' | 'ready' — 'ready' even when some probes failed
  checks: { ...INITIAL_CHECKS },
  execConnectionName: null, // first exec-capable connection, for "Run your first session"
  firstConnectionName: null, // fallback when no connection allows exec
  lastFetchedAt: null,
  forUserId: null, // whose /userinfo the snapshot was computed against
}

// Managed rules are provisioned automatically by protection profiles — only
// user-created rules prove the admin actually explored the feature.
const isUserCreated = (rule) => !rule?.managed_by

// Each probe resolves to a partial state patch. Rejections are tolerated
// per-probe (allSettled below): a failing endpoint leaves its checks false
// instead of breaking the whole widget.
const PROBES = [
  async () => {
    const { data } = await agentsService.list()
    return { checks: { agentDeployed: (data ?? []).some((a) => a.status === 'CONNECTED') } }
  },
  async () => {
    const { pages, data } = await connectionsService.getConnectionsPaginated({ pageSize: 50 })
    const connections = data ?? []
    // Mirrors the CLJS editor eligibility check (primary_connection.cljs):
    // a connection is web-terminal capable unless exec is disabled.
    const execConnection = connections.find((c) => c.access_mode_exec !== 'disabled')
    return {
      checks: { resourceCreated: (pages?.total ?? 0) >= 1 },
      execConnectionName: execConnection?.name ?? null,
      firstConnectionName: connections[0]?.name ?? null,
    }
  },
  async () => {
    const { data } = await sessionsService.list({ limit: 1 })
    return { checks: { sessionRan: (data?.total ?? 0) >= 1 } }
  },
  async () => {
    const groups = await userGroupsService.list()
    return { checks: { groupsCreated: (groups ?? []).some((g) => g !== DEFAULT_GROUP) } }
  },
  async () => {
    const { data } = await usersService.list()
    return {
      checks: {
        peopleAssigned: (data ?? []).some((u) => (u.groups ?? []).some((g) => g !== DEFAULT_GROUP)),
      },
    }
  },
  async () => {
    const { data } = await guardrailsService.list()
    return { checks: { guardrailsExplored: (data ?? []).filter(isUserCreated).length >= 1 } }
  },
  async () => {
    const { data } = await dataMaskingService.list()
    return { checks: { dataMaskingExplored: (data ?? []).filter(isUserCreated).length >= 1 } }
  },
  async () => {
    // The analyzer is configured once an AI provider exists; 404 is the
    // gateway's "never set up" answer, not a probe failure.
    try {
      await aiSessionAnalyzerService.getProvider()
      return { checks: { aiAnalyzerEnabled: true } }
    } catch (err) {
      if (err.response?.status === 404) return { checks: { aiAnalyzerEnabled: false } }
      throw err
    }
  },
]

let inFlight = null
let inFlightUserId = null

export const useConfigStatusStore = create((set, get) => ({
  ...INITIAL_STATE,

  fetchStatus: async ({ force = false } = {}) => {
    const { isAdmin, user } = useUserStore.getState()
    if (!isAdmin) return
    const userId = user?.id ?? null

    // A stale snapshot from a previous login (same tab) must never satisfy
    // the TTL — refetch whenever the authenticated user changed.
    const { lastFetchedAt, forUserId } = get()
    const sameUser = forUserId === userId
    if (!force && sameUser && lastFetchedAt && Date.now() - lastFetchedAt < TTL_MS) return

    if (inFlight) {
      if (inFlightUserId === userId) return inFlight
      // A different user's fetch is in flight — wait it out, then re-evaluate
      // from scratch (TTL, user match, and any fetch started meanwhile).
      await inFlight.catch(() => {})
      return get().fetchStatus({ force })
    }

    inFlightUserId = userId
    inFlight = (async () => {
      set((state) => ({ status: state.status === 'ready' ? 'ready' : 'loading' }))

      const results = await Promise.allSettled(PROBES.map((probe) => probe()))
      results
        .filter((r) => r.status === 'rejected')
        .forEach((r) => console.warn('[useConfigStatusStore] probe failed:', r.reason))

      // With zero probes answering (gateway unreachable, expired auth) there
      // is no snapshot to publish — keep the previous state so the widget
      // stays hidden/unchanged and the next trigger retries past the TTL.
      const fulfilled = results.filter((r) => r.status === 'fulfilled')
      if (fulfilled.length === 0) return

      const patch = { checks: {} }
      for (const result of fulfilled) {
        const { checks, ...rest } = result.value
        Object.assign(patch.checks, checks)
        Object.assign(patch, rest)
      }

      set((state) => ({
        // When the authenticated user changed, never merge over the previous
        // user's snapshot — failed probes must yield `false`, not user A's
        // leftovers stamped with user B's id.
        execConnectionName: sameUser ? state.execConnectionName : null,
        firstConnectionName: sameUser ? state.firstConnectionName : null,
        ...patch,
        checks: {
          ...(sameUser ? state.checks : INITIAL_CHECKS),
          ...patch.checks,
          // Derived synchronously from the already-loaded /userinfo payload.
          protectionLevelSet: user?.default_protection_profile != null,
        },
        status: 'ready',
        lastFetchedAt: Date.now(),
        forUserId: userId,
      }))
    })()

    try {
      await inFlight
    } finally {
      inFlight = null
      inFlightUserId = null
    }
  },
}))
