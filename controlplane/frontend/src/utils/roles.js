export const ROLE_ADMIN = 'admin'
export const ROLE_APPROVER = 'approver'
export const ROLE_STANDARD = 'standard'

export const ROLE_OPTIONS = [
  { value: ROLE_ADMIN, label: 'Administrator', description: 'Full access to the control plane' },
  { value: ROLE_APPROVER, label: 'Approver', description: 'Reviews only' },
  { value: ROLE_STANDARD, label: 'Standard', description: 'No pages in this app' }
]

export function roleLabel(role) {
  return ROLE_OPTIONS.find((r) => r.value === role)?.label ?? '—'
}

// standard is the absence of a reserved group, never a group itself. Names come
// from /serverinfo because ADMIN_USERNAME renames the admin one.
export function roleToGroups(role, adminRoleName, approverRoleName) {
  if (role === ROLE_ADMIN) return [adminRoleName]
  if (role === ROLE_APPROVER) return [approverRoleName]
  return []
}

// Admin passes everything, as isGroupAllowed does. Approver is enforced here only:
// the backend gives it standard route access, so this hides pages, it does not
// protect the API behind them.
export function hasRole(userRole, requiredRole) {
  if (!requiredRole) return true
  if (userRole === ROLE_ADMIN) return true
  return userRole === requiredRole
}
