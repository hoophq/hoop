import api from './api'

const infrastructure = {
  get: () => api.get('/serverconfig/misc'),
  update: (payload) => api.put('/serverconfig/misc', payload),
  getAnalyticsMode: () => api.get('/orgs/analytics-mode'),
  updateAnalyticsMode: (mode) => api.put('/orgs/analytics-mode', { analytics_mode: mode }),
  getHideRoleInfo: () => api.get('/orgs/hide-role-info'),
  updateHideRoleInfo: (hideRoleInfo) =>
    api.put('/orgs/hide-role-info', { hide_role_info: hideRoleInfo }),
}

export default infrastructure
