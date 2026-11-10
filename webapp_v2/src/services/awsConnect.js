import api from './api'

export const awsConnectService = {
  saveCredentials: (data) => api.put('/integrations/aws/iam/accesskeys', data),
  listAccounts: () => api.get('/integrations/aws/organizations'),
  describeDbInstances: (accountIds) =>
    api.post('/integrations/aws/rds/describe-db-instances', { account_ids: accountIds }),
  resetRootPasswords: (instanceArns) => api.post('/integrations/aws/rds/credentials', { instances: instanceArns }),
  listJobs: () => api.get('/dbroles/jobs'),
  createJob: (data) => api.post('/dbroles/jobs', data),
}
