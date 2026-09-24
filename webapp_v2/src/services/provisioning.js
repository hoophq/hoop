import api from './api'

// How a control plane learns its reviewers from the identity provider
// (ADR-0019): a SCIM token the provider pushes with, or a directory sync the
// control plane pulls with. Only one of the two may be active.
//
// `createScimToken` is the only call that returns the token; it is stored
// hashed and never shown again. The directory sync settings come back with
// secrets as "********"; sending that value back keeps the stored secret.
export const provisioningService = {
  getScim: () => api.get('/serverconfig/scim'),
  createScimToken: () => api.post('/serverconfig/scim'),
  deleteScim: () => api.delete('/serverconfig/scim'),
  getDirectorySync: () => api.get('/serverconfig/directory-sync'),
  saveDirectorySync: (data) => api.put('/serverconfig/directory-sync', data),
  deleteDirectorySync: () => api.delete('/serverconfig/directory-sync'),
  runDirectorySync: () => api.post('/serverconfig/directory-sync/run'),
  listDirectoryGroups: () => api.get('/serverconfig/directory-sync/groups'),
}
