// CLJS tags_utils/extract-label: strips the `hoop.dev/<category>.` system
// prefix from a tag key so the user sees `environment` instead of
// `hoop.dev/infrastructure.environment`. Keys that don't match the system
// pattern (free-form user tags) pass through untouched.
const HOOP_LABEL_RE = /^hoop\.dev\/[^.]+\.([^.]+)$/

export function labelForTag(key) {
  if (!key) return ''
  const m = key.match(HOOP_LABEL_RE)
  return m ? m[1] : key
}
