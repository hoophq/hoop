import api from './api'

export const runbooksService = {
  // { file_name, connection_name, repository?, parameters, correlation_id?,
  //   jira_fields?, cmdb_fields? } → { session_id, has_review, ... }
  exec: (payload) => api.post('/runbooks/exec', payload),
  // Returns the repositories+items shape from the gateway:
  //   [{ repository, commit, items: [{ name, ... }], error?, ... }, ...]
  // Mirrors what /webapp uses for the runner library and the setup rule form.
  listForConnection: async (connectionName) => {
    const { data } = await api.get('/runbooks', {
      params: connectionName ? { connection_name: connectionName } : {},
    })
    return data?.repositories || []
  },
}
