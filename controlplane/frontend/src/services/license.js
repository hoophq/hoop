import api from './api'

// PUT /orgs/license takes the document Hoop issued: {payload, key_id, signature}.
// 204 on success. 400 {message} when the signature fails or when the API_URL
// hostname is not in allowed_hosts. Replacing an installed license is allowed.
//
// There is no getInfo(): license state comes from /serverinfo through
// useUserStore.refreshServerInfo, so every reader sees one value.
export const licenseService = {
  update: (payload) => api.put('/orgs/license', payload).then((r) => r.data),
}

export default licenseService
