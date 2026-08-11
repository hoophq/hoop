// Display helpers for the signed-in user. Lifted out of layout/Sidebar/helpers.js
// when the user menu moved from the sidebar footer into the global header.

const MAX_DISPLAY_NAME_LENGTH = 20

export function getUserInitials(user) {
  if (!user) return '?'
  const name = user.name || user.email || ''
  return name
    .split(' ')
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0].toUpperCase())
    .join('')
}

export function getUserDisplayName(user) {
  return (user?.name || user?.email || 'User').slice(0, MAX_DISPLAY_NAME_LENGTH)
}
