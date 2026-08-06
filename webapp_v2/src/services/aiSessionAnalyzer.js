import api from './api'

// Rule names are user-supplied and travel in the path, so every interpolation
// goes through encodeURIComponent.
const rulePath = (name) => `/ai/session-analyzer/rules/${encodeURIComponent(name)}`

export const aiSessionAnalyzerService = {
  // 200 with the provider when configured; 404 when the org never set one up.
  getProvider: () => api.get('/ai/session-analyzer/providers'),
  // Upsert — the gateway keeps a single provider record per org.
  saveProvider: (payload) => api.post('/ai/session-analyzer/providers', payload),
  removeProvider: () => api.delete('/ai/session-analyzer/providers'),

  // Omitting `page_size` returns every rule: the handler reads 0 as "no
  // pagination" (gateway/api/ai/ai.go:240). The response is the paginated
  // envelope { pages, data } either way.
  listRules: () => api.get('/ai/session-analyzer/rules'),
  getRule: (name) => api.get(rulePath(name)),
  createRule: (payload) => api.post('/ai/session-analyzer/rules', payload),
  updateRule: (name, payload) => api.put(rulePath(name), payload),
  removeRule: (name) => api.delete(rulePath(name)),

  getSystemPrompt: () => api.get('/ai/session-analyzer/system-prompt'),

  // 200 with the rule that resolves for the connection; 404 when none applies.
  getConnectionRule: (nameOrID) =>
    api.get(`/connections/${encodeURIComponent(nameOrID)}/ai-session-analyzer-rule`),
}
