import api from './api'

export const runbooksService = {
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
