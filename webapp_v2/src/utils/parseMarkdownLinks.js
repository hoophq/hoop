// Splits text into segments separating inline `[label](url)` links from
// plain text. Returns
//   [{ type: 'text', value }, { type: 'link', value, url }, ...]
const LINK_RE = /\[([^\]]+)\]\(([^)]+)\)/g

export function parseMarkdownLinks(text) {
  if (text == null || text === '') return []
  const str = String(text)
  const segments = []
  let lastIndex = 0
  let match
  LINK_RE.lastIndex = 0
  while ((match = LINK_RE.exec(str)) !== null) {
    if (match.index > lastIndex) {
      segments.push({ type: 'text', value: str.slice(lastIndex, match.index) })
    }
    segments.push({ type: 'link', value: match[1], url: match[2] })
    lastIndex = match.index + match[0].length
  }
  if (lastIndex < str.length) {
    segments.push({ type: 'text', value: str.slice(lastIndex) })
  }
  return segments
}
