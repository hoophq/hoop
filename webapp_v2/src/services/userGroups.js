import api from './api'

export const userGroupsService = {
  // Bare string array, sorted, unioning the identity side (users, service
  // accounts, API keys, AI agents) with the access_control plugin config. Empty
  // organizations return `[]`; older gateways return `null`, so callers still
  // coalesce.
  list: () => api.get('/users/groups'),
  // Only `name` is read from the request body — the handler binds
  // openapi.UserGroup, which has no other field, so a description would be
  // accepted and silently dropped. Responds 409 when the group already exists.
  create: (data) => api.post('/users/groups', data),
  // 204 on success, 422 for the built-in admin group. Group names are free
  // text, so the path segment has to be encoded.
  remove: (name) => api.delete(`/users/groups/${encodeURIComponent(name)}`),
}
