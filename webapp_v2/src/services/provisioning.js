import api from './api'

// How a control plane learns its reviewers without anyone logging in
// (ADR-0019): a Slack directory sync the control plane pulls with. SCIM is the
// next source; the file import lives in services/users.js.
//
// `getStatus` says whether the Slack import manages the org's groups;
// `stopManagingGroups` hands them back to login and the Users page, and is
// refused while the sync exists.
export const provisioningService = {
  getDirectorySync: () => api.get('/serverconfig/directory-sync'),
  saveDirectorySync: (data) => api.put('/serverconfig/directory-sync', data),
  deleteDirectorySync: () => api.delete('/serverconfig/directory-sync'),
  runDirectorySync: () => api.post('/serverconfig/directory-sync/run'),
  listDirectoryGroups: () => api.get('/serverconfig/directory-sync/groups'),
  getStatus: () => api.get('/serverconfig/provisioning'),
  stopManagingGroups: () => api.delete('/serverconfig/provisioning'),
}
