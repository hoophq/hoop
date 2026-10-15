import api from './api'

export const usersService = {
  // Every user as a plain array. Pickers that filter client-side use it.
  list: () => api.get('/users'),
  // One page as `{ pages: { total, page, size }, data }`. Empty filters are not sent.
  listPaginated: ({ page, pageSize, email, status }) =>
    api.get('/users', {
      params: { page, page_size: pageSize, email: email || undefined, status: status || undefined },
    }),
  get: (id) => api.get(`/users/${id}`),
  create: (data) => api.post('/users', data),
  update: (id, data) => api.put(`/users/${id}`, data),
  listGroups: () => api.get('/users/groups'),
}
