export const ROLE_ADMIN = 'admin'
export const ROLE_APPROVER = 'approver'
// /userinfo reports it for the auditor group when the user is not admin or approver.
export const ROLE_AUDITOR = 'auditor'
export const ROLE_STANDARD = 'standard'

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
