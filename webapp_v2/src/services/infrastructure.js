import api from './api'

const infrastructure = {
  get: () => api.get('/serverconfig/misc').then((r) => r.data),
  update: (payload) => api.put('/serverconfig/misc', payload).then((r) => r.data),
  getAnalyticsMode: () => api.get('/orgs/analytics-mode').then((r) => r.data),
  updateAnalyticsMode: (mode) =>
    api.put('/orgs/analytics-mode', { analytics_mode: mode }).then((r) => r.data),
}

export default infrastructure
