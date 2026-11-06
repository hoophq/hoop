import api from './api'

export const jiraTemplatesService = {
  list: () => api.get('/integrations/jira/issuetemplates'),
  get: (id) => api.get(`/integrations/jira/issuetemplates/${id}`),
  create: (payload) => api.post('/integrations/jira/issuetemplates', payload),
  update: (id, payload) => api.put(`/integrations/jira/issuetemplates/${id}`, payload),
  remove: (id) => api.delete(`/integrations/jira/issuetemplates/${id}`),

  // The body is null (not 404) when the org has no Jira integration configured.
  getIntegration: () => api.get('/integrations/jira'),
  createIntegration: (payload) => api.post('/integrations/jira', payload),
  updateIntegration: (payload) => api.put('/integrations/jira', payload),

  // object_type_id and a non-zero limit are required by the API (422 otherwise).
  // An aql expression replaces the object type scope on the gateway.
  searchAssetObjects: ({ objectTypeId, objectSchemaId, name, aql, limit = 50, offset = 0 }) =>
    api.get('/integrations/jira/assets/objects', {
      params: {
        object_type_id: objectTypeId,
        limit,
        offset,
        ...(name ? { name } : {}),
        ...(objectSchemaId ? { object_schema_id: objectSchemaId } : {}),
        ...(aql ? { aql } : {}),
      },
    }),

  // The AQL filters of the Assets custom fields; fields without one are omitted.
  getAssetFieldConfigs: (jiraFields) =>
    api.get('/integrations/jira/assets/fieldconfigs', {
      params: { jira_fields: jiraFields.join(',') },
    }),
}
