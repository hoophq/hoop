export function getUserInitials(user) {
  if (!user) return '?';
  const name = user.name || user.email || '';
  return name
    .split(' ')
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0].toUpperCase())
    .join('');
}

export function shouldHide(item, isAdmin, isSelfHosted = false) {
  if (item.adminOnly && !isAdmin) return true;
  if (item.selfhostedOnly && !isSelfHosted) return true;
  return false;
}

export function isBlocked(item, isFreeLicense) {
  return isFreeLicense && item.freeFeature === false;
}

export function isActive(path, pathname) {
  if (!path) return false;
  if (path === '/dashboard') return pathname === '/dashboard' || pathname === '/';
  return pathname === path || pathname.startsWith(path + '/');
}
