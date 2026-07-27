export function truncateKey(key) {
  if (!key) return '—'
  return key.length > 12 ? `${key.slice(0, 10)}...` : key
}
