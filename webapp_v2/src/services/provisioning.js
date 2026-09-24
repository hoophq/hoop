import api from './api'

// How a control plane learns its reviewers without anyone logging in
// (ADR-0020): the Slack import, which the control plane runs on an interval.
// SCIM is the next source. Deleting the import keeps the users and groups it
// wrote, and hands the groups back to SSO login.
export const provisioningService = {
  getDirectorySync: () => api.get('/serverconfig/directory-sync'),
  saveDirectorySync: (data) => api.put('/serverconfig/directory-sync', data),
  deleteDirectorySync: () => api.delete('/serverconfig/directory-sync'),
  runDirectorySync: () => api.post('/serverconfig/directory-sync/run'),
  listDirectoryGroups: () => api.get('/serverconfig/directory-sync/groups'),
}
