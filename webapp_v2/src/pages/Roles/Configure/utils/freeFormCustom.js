// Mirrors gateway/api/connections/secrets.go::isFreeFormCustom — keep in
// sync with the backend predicate so the UI's rendering decision lines
// up with whether values come back stripped or plaintext.
//
// A "free-form custom" connection has type=custom AND no predefined
// credential schema (subtype empty or in the CLJS exclusion set from
// credentials_tab.cljs:14-20). For these, envvars are user data —
// not credentials — and round-trip in plaintext.
const FREE_FORM_SUBTYPES = new Set([
  'tcp',
  'httpproxy',
  'ssh',
  'linux-vm',
  'claude-code',
])

export function isFreeFormCustom(connection) {
  if (!connection || connection.type !== 'custom') return false
  const sub = connection.subtype || ''
  if (sub === '') return true
  return FREE_FORM_SUBTYPES.has(sub)
}
