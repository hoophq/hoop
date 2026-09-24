import api from './api'

// `importFile` sends a CSV (email,name,groups; groups separated by ";") to the
// control plane's file import. A bad row fails that row, not the file; the
// response lists the rows that failed.
export const usersService = {
  list: () => api.get('/users'),
  get: (id) => api.get(`/users/${id}`),
  create: (data) => api.post('/users', data),
  update: (id, data) => api.put(`/users/${id}`, data),
  listGroups: () => api.get('/users/groups'),
  importFile: (file, deactivateMissing) => {
    const form = new FormData()
    form.append('file', file)
    form.append('deactivate_missing', deactivateMissing ? 'true' : 'false')
    return api.post('/users/import', form)
  },
}
