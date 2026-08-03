import api from './api'

export const userGroupsService = {
  // The gateway returns a bare string array, and `null` — not `[]` — when the
  // organization has no groups. Callers must coalesce.
  list: () => api.get('/users/groups'),
}
