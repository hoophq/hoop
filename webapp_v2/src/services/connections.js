import api from './api'

export const connectionsService = {
  getConnections: () => api.get('/connections').then((res) => res.data),

  // Non-paginated lookup by id. NOT the paginated endpoint: pagination adds an
  // access-group (RBAC) join that can exclude a connection the user can see,
  // which would drop selected labels. `includeSidecarMirrors` adds the
  // connections that mirror a sidecar listener, which every list leaves out
  // by default.
  getConnectionsByIds: (ids = [], { includeSidecarMirrors = false } = {}) => {
    if (!ids.length) return Promise.resolve([])
    const params = new URLSearchParams({ connection_ids: ids.join(',') })
    if (includeSidecarMirrors) params.set('include_sidecar_mirrors', 'true')
    return api.get(`/connections?${params.toString()}`).then((res) => res.data)
  },

  // Every connection that mirrors a sidecar listener, and nothing else.
  getSidecarMirrors: () =>
    api
      .get('/connections?include_sidecar_mirrors=true&managed_by=sidecar')
      .then((res) => res.data),

  // Returns the paginated envelope { pages, data }.
  getConnectionsPaginated: ({
    page = 1,
    pageSize = 50,
    search,
    connectionIds,
    includeSidecarMirrors = false,
  } = {}) => {
    const params = new URLSearchParams({
      page: String(page),
      page_size: String(pageSize),
    })
    if (search) params.set('search', search)
    if (connectionIds?.length) params.set('connection_ids', connectionIds.join(','))
    if (includeSidecarMirrors) params.set('include_sidecar_mirrors', 'true')
    return api.get(`/connections?${params.toString()}`).then((res) => res.data)
  },
  getConnection: (nameOrId) =>
    api.get(`/connections/${encodeURIComponent(nameOrId)}`).then((res) => res.data),
  patchConnection: (name, payload) =>
    api
      .patch(`/connections/${encodeURIComponent(name)}`, payload)
      .then((res) => res.data),
  deleteConnection: (name) =>
    api.delete(`/connections/${encodeURIComponent(name)}`),
  testConnection: (name) =>
    api.get(`/connections/${encodeURIComponent(name)}/test`).then((res) => res.data),

  // MCP OAuth login flow (create/edit page "Authorize with MCP" widget).
  // `redirect` is the app URL the gateway callback sends the popup back to,
  // carrying ?mcp_oauth=success|error&flow_id=...
  mcpOAuthAuthorize: (payload, redirect) =>
    api
      .post(`/mcp-oauth/authorize?redirect=${encodeURIComponent(redirect)}`, payload)
      .then((res) => res.data),
  // Single-use: the gateway consumes the flow on read.
  mcpOAuthToken: (flowId) =>
    api.get(`/mcp-oauth/token/${encodeURIComponent(flowId)}`).then((res) => res.data),

  // Built-in catalog of publicly hosted remote MCP servers, used by the MCP
  // Gateway server picker to pre-fill endpoint, transport and auth mode.
  // Static build-time data on the gateway; read-only role, not admin-gated.
  mcpCatalog: () => api.get('/mcp-catalog').then((res) => res.data),
}
