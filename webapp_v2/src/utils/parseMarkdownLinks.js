// Splits a string into an array of segments, separating inline markdown
// links of the form `[label](url)` from plain text. No other markdown is
// recognised — descriptions sourced from connections-metadata.json only
// carry inline links.
//
// Returns:
//   [{ type: 'text', value: '...' }, { type: 'link', value: 'label', url: '...' }, ...]
//
// Mirrors the CLJS reference at
// webapp/src/webapp/components/text_with_markdown_link.cljs so behaviour
// stays in sync while both apps coexist.
const LINK_RE = /\[([^\]]+)\]\(([^)]+)\)/g

export function parseMarkdownLinks(text) {
  if (text == null || text === '') return []
  const str = String(text)
  const segments = []
  let lastIndex = 0
  let match
  // Reset lastIndex defensively even though we re-create the regex above.
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
