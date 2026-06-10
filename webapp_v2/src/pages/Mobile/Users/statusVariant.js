// Mirrors the desktop mapping in pages/Organization/Users.
export function statusVariant(status) {
  if (status === 'active') return 'active'
  if (status === 'inactive') return 'inactive'
  return 'warning'
}
