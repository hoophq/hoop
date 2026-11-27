import api from './api'

export const orgsService = {
  respondToInvitation: (action) => api.post('/orgs/invitations', { action }),
}
