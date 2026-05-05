import api from './api'

export const auditLogsService = {
  list: ({ page = 1, pageSize = 25, actorEmail, createdAfter, createdBefore } = {}) => {
    const params = { page, page_size: pageSize }
    if (actorEmail) params.actor_email = actorEmail
    if (createdAfter) params.created_after = createdAfter
    if (createdBefore) params.created_before = createdBefore
    return api.get('/audit/logs', { params })
  },
}
