import api from './api'

export const pluginsService = {
  // GET /plugins/:name returns envvars unredacted (base64); the list endpoint redacts them.
  get: (name) => api.get(`/plugins/${name}`),
  create: (data) => api.post('/plugins', data),
  // The gateway reads the plugin name from the body, not the path — `data` must include `name`.
  // The connections array is replaced wholesale: omitted connections are removed.
  update: (name, data) => api.put(`/plugins/${name}`, data),
  // Body is a bare map of envvar name → base64-encoded value; replaces the whole envvars map.
  updateConfig: (name, envvars) => api.put(`/plugins/${name}/config`, envvars),
}
