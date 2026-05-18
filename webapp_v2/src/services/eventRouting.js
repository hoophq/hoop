import api from './api'

// ── Normalisation ────────────────────────────────────────────────────────
// Backend uses snake_case JSON. We normalise to camelCase at the boundary
// so the rest of the React app stays idiomatic.

function normalizeSubscription(raw) {
  if (!raw) return null
  return {
    id: raw.id,
    name: raw.name,
    description: raw.description || '',
    status: raw.status,
    eventTypes: raw.event_types || [],
    runbookRepository: raw.runbook_repository || '',
    runbookFile: raw.runbook_file || '',
    connectionId: raw.connection_id,
    connectionName: raw.connection_name,
    parameterMapping: raw.parameter_mapping || {},
    createdByEmail: raw.created_by_email,
    createdAt: raw.created_at,
    updatedAt: raw.updated_at,
    deliveredCount7d: raw.delivered_count_7d || 0,
    failedCount7d: raw.failed_count_7d || 0,
    lastError: raw.last_error || null,
  }
}

function normalizeEventType(raw) {
  if (!raw) return null
  return {
    name: raw.name,
    category: (raw.category || '').toLowerCase(),
    description: raw.description || '',
    schema: raw.schema || [],
    samplePayload: raw.sample_payload || {},
  }
}

function normalizeDispatch(raw) {
  if (!raw) return null
  return {
    id: raw.id,
    eventId: raw.event_id,
    eventType: raw.event_type,
    status: raw.status,
    attempt: raw.attempt,
    sessionId: raw.session_id || null,
    lastError: raw.last_error || null,
    replayedFrom: raw.replayed_from || null,
    durationMs: raw.duration_ms || null,
    occurredAt: raw.occurred_at,
    createdAt: raw.created_at,
    dispatchedAt: raw.dispatched_at || null,
    finishedAt: raw.finished_at || null,
  }
}

function toRequestBody(sub) {
  const body = {
    name: sub.name,
    description: sub.description || '',
    event_types: sub.eventTypes,
    runbook_repository: sub.runbookRepository,
    runbook_file: sub.runbookFile,
    connection_name: sub.connectionName,
    parameter_mapping: sub.parameterMapping || {},
  }
  if (sub.status) body.status = sub.status
  return body
}

export const eventRoutingService = {
  // Catalog
  listCatalog: async () => {
    const { data } = await api.get('/event-routing/catalog')
    return (data || []).map(normalizeEventType)
  },
  getCatalogEntry: async (eventType) => {
    const { data } = await api.get(`/event-routing/catalog/${encodeURIComponent(eventType)}`)
    return normalizeEventType(data)
  },

  // Subscriptions
  listSubscriptions: async () => {
    const { data } = await api.get('/event-routing/subscriptions')
    return (data || []).map(normalizeSubscription)
  },
  getSubscription: async (id) => {
    const { data } = await api.get(`/event-routing/subscriptions/${id}`)
    return normalizeSubscription(data)
  },
  createSubscription: async (sub) => {
    const { data } = await api.post('/event-routing/subscriptions', toRequestBody(sub))
    return normalizeSubscription(data)
  },
  updateSubscription: async (id, sub) => {
    const { data } = await api.put(`/event-routing/subscriptions/${id}`, toRequestBody(sub))
    return normalizeSubscription(data)
  },
  deleteSubscription: (id) => api.delete(`/event-routing/subscriptions/${id}`),
  pauseSubscription: (id) => api.post(`/event-routing/subscriptions/${id}/pause`),
  resumeSubscription: (id) => api.post(`/event-routing/subscriptions/${id}/resume`),

  // Dispatches
  listDispatches: async (subId, params = {}) => {
    const { data } = await api.get(`/event-routing/subscriptions/${subId}/dispatches`, { params })
    return {
      items: (data?.items || []).map(normalizeDispatch),
      total: data?.total || 0,
      page: data?.page || 1,
      limit: data?.limit || 50,
    }
  },
  getDispatch: async (id) => {
    const { data } = await api.get(`/event-routing/dispatches/${id}`)
    return normalizeDispatch(data)
  },
  replayDispatch: async (id) => {
    const { data } = await api.post(`/event-routing/dispatches/${id}/replay`)
    return data
  },
}
