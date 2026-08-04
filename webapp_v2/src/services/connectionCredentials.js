import api from './api'

/**
 * Native-access credentials.
 *
 * Kept out of services/connections.js: that file is the connection-CRUD domain,
 * while credentials are a separate resource with their own lifecycle (issue,
 * resume after review, close, revoke) and their own list endpoint.
 */
export const connectionCredentialsService = {
  // Secret-less list of every active credential owned by the caller. This is
  // the source of truth for "which rows have a live session" — the per-connection
  // GET below is the only endpoint that returns secrets, and it is only called
  // when the user actually expands a row.
  listActive: () => api.get('/connection-credentials').then((res) => res.data),

  get: (connectionName) =>
    api.get(`/connections/${connectionName}/credentials`).then((res) => res.data),

  // Omitting accessDurationSec issues a persistent credential (expire_at null).
  // Review-required connections reject that and need an explicit window.
  create: (connectionName, accessDurationSec) =>
    api
      .post(
        `/connections/${connectionName}/credentials`,
        accessDurationSec ? { access_duration_seconds: accessDurationSec } : {}
      )
      .then((res) => ({ status: res.status, data: res.data })),

  // Issues the credential for an approved review. Still answers 202 while the
  // review is pending, and 403 once it has been rejected.
  resume: (connectionName, sessionId, accessDurationSec) =>
    api
      .post(`/connections/${connectionName}/credentials/${sessionId}`, {
        access_duration_seconds: accessDurationSec,
      })
      .then((res) => ({ status: res.status, data: res.data })),

  // Ends the audit session and tears down live proxy connections, but keeps the
  // token: reconnecting returns the same secret (the gateway's stable-key
  // contract). Use revoke to invalidate the token itself.
  close: (connectionName, credentialId) =>
    api.post(`/connections/${connectionName}/credentials/${credentialId}/close`),

  revoke: (connectionName, credentialId) =>
    api.post(`/connections/${connectionName}/credentials/${credentialId}/revoke`),
}
