export const ROLE_ADMIN = 'admin'
export const ROLE_APPROVER = 'approver'
export const ROLE_STANDARD = 'standard'

export const ROLE_OPTIONS = [
  { value: ROLE_ADMIN, label: 'Administrator', description: 'Full access to the control plane' },
  { value: ROLE_APPROVER, label: 'Approver', description: 'Reviews only' }
]

// Not offered by the picker, but an IdP can sync a user in neither group.
export function roleLabel(role) {
  if (role === ROLE_STANDARD) return 'Standard'
  return ROLE_OPTIONS.find((r) => r.value === role)?.label ?? '—'
}
