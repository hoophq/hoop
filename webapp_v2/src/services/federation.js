import api from './api'

const base = (name) => `/connections/${encodeURIComponent(name)}/federation`

export const federationService = {
  get: (name) => api.get(base(name)),
  save: (name, config) => api.put(base(name), config),
  remove: (name) => api.delete(base(name)),
  test: (payload) => api.post('/federation/test', payload),
  oauthAuthorize: (name, redirect) =>
    api.get(`${base(name)}/oauth/authorize`, { params: { redirect } }),
  oauthStatus: (name) => api.get(`${base(name)}/oauth`),
  oauthDisconnect: (name) => api.delete(`${base(name)}/oauth`),
}
