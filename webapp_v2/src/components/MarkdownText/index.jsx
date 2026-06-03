import { Text, Anchor } from '@mantine/core'
import { parseMarkdownLinks } from '@/utils/parseMarkdownLinks'

// Mantine <Text> drop-in that renders inline `[label](url)` segments
// as Anchors. No other markdown is interpreted.
export default function MarkdownText({
  children,
  size = 'xs',
  c = 'dimmed',
  ...rest
}) {
  const segments = parseMarkdownLinks(children)
  if (segments.length === 0) return null
  return (
    <Text size={size} c={c} {...rest}>
      {segments.map((seg, i) =>
        seg.type === 'link' ? (
          <Anchor
            key={i}
            href={seg.url}
            target="_blank"
            rel="noopener noreferrer"
            size={size}
          >
            {seg.value}
          </Anchor>
        ) : (
          seg.value
        ),
      )}
    </Text>
  )
}
