export const STEPS = [
  { id: 'credentials', label: 'Credentials' },
  { id: 'accounts', label: 'Accounts' },
  { id: 'resources', label: 'Resources' },
  { id: 'review', label: 'Review and Create' },
  { id: 'status', label: 'Status' },
]

export const AUTH_GATEWAY_PROFILE = 'gateway-profile'
export const AUTH_CREDENTIALS = 'aws-credentials'

export const AWS_REGIONS = [
  'us-east-1',
  'us-east-2',
  'us-west-1',
  'us-west-2',
  'af-south-1',
  'ap-east-1',
  'ap-south-1',
  'ap-northeast-1',
  'ap-northeast-2',
  'ap-northeast-3',
  'ap-southeast-1',
  'ap-southeast-2',
  'ap-southeast-3',
  'ap-southeast-4',
  'ca-central-1',
  'ca-west-1',
  'eu-central-1',
  'eu-central-2',
  'eu-west-1',
  'eu-west-2',
  'eu-west-3',
  'eu-south-1',
  'eu-south-2',
  'eu-north-1',
  'il-central-1',
  'me-central-1',
  'me-south-1',
  'sa-east-1',
  'us-gov-east-1',
  'us-gov-west-1',
]

export function credentialsBody(authMethod, credentials) {
  if (authMethod === AUTH_GATEWAY_PROFILE) return { region: credentials.region }
  return {
    access_key_id: credentials.accessKeyId,
    secret_access_key: credentials.secretAccessKey,
    region: credentials.region,
    session_token: credentials.sessionToken,
  }
}

const isConnected = (instance) => instance.connection_resources?.length > 0

// An account whose only item carries an error shows that error and no children.
export function buildResourceTree(instances, accounts, skipConnected) {
  const source = skipConnected ? instances.filter((i) => !isConnected(i)) : instances
  const byAccount = {}
  for (const instance of source) (byAccount[instance.account_id] ??= []).push(instance)

  const tree = accounts.map((account) => {
    const items = byAccount[account.account_id] ?? []
    const error = items.length === 1 && items[0].error ? items[0].error : null
    return {
      id: account.account_id,
      name: account.name,
      status: account.status,
      isAccount: true,
      error,
      children: error
        ? []
        : items.map((instance) => ({
            id: instance.arn,
            name: instance.name,
            status: instance.status,
            engine: instance.engine,
            accountId: account.account_id,
          })),
    }
  })

  return {
    tree: skipConnected ? tree.filter((account) => account.error || account.children.length > 0) : tree,
    connected: instances.filter((i) => isConnected(i) && !i.error),
  }
}

export const resourceIds = (tree) => tree.flatMap((account) => account.children.map((child) => child.id))

export function accountChecked(account, selected) {
  const picked = account.children.filter((child) => selected.includes(child.id)).length
  if (picked === 0) return false
  return picked === account.children.length ? true : 'some'
}

// The free plan picks one resource: a new pick replaces the old one.
export function toggleResource(selected, row, checked, freeLicense) {
  const ids = row.isAccount ? row.children.map((child) => child.id) : [row.id]
  if (!checked) return selected.filter((id) => !ids.includes(id))
  if (freeLicense) return row.isAccount ? selected : [row.id]
  return [...new Set([...selected, ...ids])]
}

export function selectedTree(tree, selected) {
  return tree
    .map((account) => ({ ...account, children: account.children.filter((child) => selected.includes(child.id)) }))
    .filter((account) => account.children.length > 0)
}

export const defaultRoleName = (resource) => `${resource.name}-${resource.accountId}`

export const roleName = (resource, roleNames) => roleNames[resource.id] ?? defaultRoleName(resource)

export function jobPayload(resource, state) {
  const cidr = state.securityGroups[resource.id] ?? ''
  const payload = {
    agent_id: state.agentAssignments[resource.id],
    aws: {
      instance_arn: resource.id,
      default_security_group: cidr ? { ingress_cidr: cidr } : null,
    },
    connection_prefix_name: `${roleName(resource, state.roleNames)}-`,
    job_steps: state.createConnection ? ['create-connections', 'send-webhook'] : ['send-webhook'],
  }
  if (state.vaultEnabled && state.vaultPath) payload.vault_provider = { secret_id: state.vaultPath }
  return payload
}

const STATUS_ORDER = { pending: 0, failure: 1, success: 2 }

export function statusRows(jobs) {
  return Object.values(jobs)
    .map((job) => ({
      id: job.resource.id,
      name: job.name,
      engine: job.resource.engine,
      status: job.status,
      error: job.error,
    }))
    .sort((a, b) => (STATUS_ORDER[a.status] ?? 3) - (STATUS_ORDER[b.status] ?? 3))
}

export function downloadCredentialsFile(credentials) {
  const blob = new Blob([JSON.stringify(credentials, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'hoop-aws-database-credentials.json'
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}
