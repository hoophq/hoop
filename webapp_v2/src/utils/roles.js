export const ROLE_ADMIN = 'admin'
export const ROLE_APPROVER = 'approver'
export const ROLE_STANDARD = 'standard'

// Only admin maps to a group: its name comes from /serverinfo because
// ADMIN_USERNAME renames it. Approver is derived from a user's groups
// (ADR-0019), so it names none, and standard is the absence of both.
export function roleToGroups(role, adminRoleName) {
  if (role === ROLE_ADMIN) return [adminRoleName]
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
