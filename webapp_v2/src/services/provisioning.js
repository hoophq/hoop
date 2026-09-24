import api from './api'

// How a control plane learns its reviewers without anyone logging in
// (ADR-0019): a Slack directory sync the control plane pulls with, or a SCIM
// token an identity provider pushes with. Only one of the two may be active.
//
// `putScimToken` generates the token, or rotates it in one write; it is the
// only call that returns it, and it is stored hashed. `getStatus` says whether
// a source manages the org's groups; `stopManagingGroups` hands them back to
// login and the Users page, and is refused while a token or a sync exists.
export const provisioningService = {
  getScim: () => api.get('/serverconfig/scim'),
  putScimToken: () => api.put('/serverconfig/scim'),
  deleteScim: () => api.delete('/serverconfig/scim'),
  getDirectorySync: () => api.get('/serverconfig/directory-sync'),
  saveDirectorySync: (data) => api.put('/serverconfig/directory-sync', data),
  deleteDirectorySync: () => api.delete('/serverconfig/directory-sync'),
  runDirectorySync: () => api.post('/serverconfig/directory-sync/run'),
  listDirectoryGroups: () => api.get('/serverconfig/directory-sync/groups'),
  getStatus: () => api.get('/serverconfig/provisioning'),
  stopManagingGroups: () => api.delete('/serverconfig/provisioning'),
}
