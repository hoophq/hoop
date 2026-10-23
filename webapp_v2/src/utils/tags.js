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

// TagsEditor rules for agent tags. They follow the gateway rules in
// gateway/api/validation/tags.go: the gateway still has the last word (a key
// cannot end with a separator), the editor only stops what can never pass.
export const AGENT_TAG_RULES = {
  keyPattern: /^[a-zA-Z0-9_./-]{1,64}$/,
  keyError: 'Only letters, numbers, (_), (-), (.) and (/) are allowed, up to 64 characters',
  valuePattern: /^[a-zA-Z0-9\-_+=@/:\s]{1,256}$/,
  valueError: 'Only letters, numbers, spaces, (-), (_), (+), (=), (@), (/) and (:) are allowed, up to 256 characters',
  maxTags: 10,
  warnPendingDraft: true,
}
