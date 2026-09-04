import api from './api'

export const userGroupsService = {
  // Bare string array, sorted, unioning the identity side (users, service
  // accounts, API keys, AI agents) with the access_control plugin config. Empty
  // organizations return `[]`; older gateways return `null`, so callers still
  // coalesce.
  //
  // Read only. create and remove used to live here and were deleted: the
  // control plane serves POST /users/groups and DELETE /users/groups/:name,
  // but this app does not manage access-control groups (see CLAUDE.md). The
  // read stays because a review rule names its approvers by group.
  list: () => api.get('/users/groups'),
}
