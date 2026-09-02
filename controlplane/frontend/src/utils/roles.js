// The control plane's two roles, mirroring gateway/api/openapi RoleType.
export const ROLE_ADMIN = 'admin'
export const ROLE_APPROVER = 'approver'
export const ROLE_STANDARD = 'standard'

export const ROLE_OPTIONS = [
  { value: ROLE_ADMIN, label: 'Administrator', description: 'Full access to the control plane' },
  { value: ROLE_APPROVER, label: 'Approver', description: 'Reviews only' }
]

// Admin passes every route an approver passes, as it does in
// gateway/api/apiroutes/roles.go.
export function hasRole(userRole, requiredRole) {
  if (!requiredRole) return true
  if (userRole === ROLE_ADMIN) return true
  return userRole === requiredRole
}
