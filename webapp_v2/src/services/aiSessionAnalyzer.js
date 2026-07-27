import api from './api'

export const aiSessionAnalyzerService = {
  // 200 with the provider when configured; 404 when the org never set one up.
  getProvider: () => api.get('/ai/session-analyzer/providers'),
}
