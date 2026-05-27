import api from './api'

export const jiraTemplatesService = {
  list: () => api.get('/integrations/jira/issuetemplates').then((res) => res.data),
}
