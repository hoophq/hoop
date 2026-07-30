import api from './api'

export const jiraTemplatesService = {
  list: () => api.get('/integrations/jira/issuetemplates').then((res) => res.data),
  get: (id) =>
    api.get(`/integrations/jira/issuetemplates/${id}`).then((res) => res.data),
  create: (payload) =>
    api.post('/integrations/jira/issuetemplates', payload).then((res) => res.data),
  update: (id, payload) =>
    api
      .put(`/integrations/jira/issuetemplates/${id}`, payload)
      .then((res) => res.data),
  remove: (id) => api.delete(`/integrations/jira/issuetemplates/${id}`),

  // Returns null (not 404) when the org has no Jira integration configured.
  getIntegration: () => api.get('/integrations/jira').then((res) => res.data),
  createIntegration: (payload) =>
    api.post('/integrations/jira', payload).then((res) => res.data),
  updateIntegration: (payload) =>
    api.put('/integrations/jira', payload).then((res) => res.data),

  // object_type_id and a non-zero limit are required by the API (422 otherwise).
  searchAssetObjects: ({ objectTypeId, objectSchemaId, name, limit = 50, offset = 0 }) =>
    api
      .get('/integrations/jira/assets/objects', {
        params: {
          object_type_id: objectTypeId,
          limit,
          offset,
          ...(name ? { name } : {}),
          ...(objectSchemaId ? { object_schema_id: objectSchemaId } : {}),
        },
      })
      .then((res) => res.data),
}
