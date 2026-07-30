import api from './api'

export const connectionsService = {
  getConnections: () => api.get('/connections').then((res) => res.data),

  // Non-paginated lookup by id. NOT the paginated endpoint: pagination adds an
  // access-group (RBAC) join that can exclude a connection the user can see,
  // which would drop selected labels.
  getConnectionsByIds: (ids = []) => {
    if (!ids.length) return Promise.resolve([])
    const params = new URLSearchParams({ connection_ids: ids.join(',') })
    return api.get(`/connections?${params.toString()}`).then((res) => res.data)
  },

  // Returns the paginated envelope { pages, data }.
  getConnectionsPaginated: ({ page = 1, pageSize = 50, search, connectionIds } = {}) => {
    const params = new URLSearchParams({
      page: String(page),
      page_size: String(pageSize),
    })
    if (search) params.set('search', search)
    if (connectionIds?.length) params.set('connection_ids', connectionIds.join(','))
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
}
