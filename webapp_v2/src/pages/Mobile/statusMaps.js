const REVIEW_STATUS_VARIANTS = {
  PENDING: 'warning',
  APPROVED: 'active',
  REJECTED: 'danger',
  REVOKED: 'inactive',
  PROCESSING: 'warning',
  EXECUTED: 'inactive',
}

export function reviewStatusVariant(status) {
  return REVIEW_STATUS_VARIANTS[status] ?? 'inactive'
}

const SESSION_STATUS_BADGES = {
  open: { variant: 'active', label: 'Running' },
  ready: { variant: 'warning', label: 'Ready' },
  done: { variant: 'inactive', label: 'Done' },
}

export function sessionStatusBadge(status) {
  return SESSION_STATUS_BADGES[status] ?? { variant: 'inactive', label: status ?? '—' }
}
