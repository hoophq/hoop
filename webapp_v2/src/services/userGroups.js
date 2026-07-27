import api from './api'

export const userGroupsService = {
  list: () => api.get('/users/groups').then((res) => res.data),
}
