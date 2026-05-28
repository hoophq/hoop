import api from './api'

export const rulepacksService = {
  list: (search) => {
    const trimmed = (search ?? '').trim()
    const url = trimmed
      ? `/rulepacks?search=${encodeURIComponent(trimmed)}`
      : '/rulepacks'
    return api.get(url)
  },
  get: (id) => api.get(`/rulepacks/${id}`),
  apply: (id, connectionNames) =>
    api.post(`/rulepacks/${id}/apply`, { connection_names: connectionNames }),
}
