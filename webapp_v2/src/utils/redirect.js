// samePagePath is url as a path of this app (path, query and hash), or null
// when it is empty or points anywhere else. A return address after login
// never leaves the app: ?next= is read from a URL anyone can craft.
export function samePagePath(url) {
  if (!url) return null
  try {
    const u = new URL(url, window.location.origin)
    if (u.origin !== window.location.origin) return null
    return u.pathname + u.search + u.hash
  } catch {
    return null
  }
}
