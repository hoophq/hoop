import { Text, Anchor } from '@mantine/core'
import { parseMarkdownLinks } from '@/utils/parseMarkdownLinks'

// Mantine <Text> drop-in that renders inline markdown links found in the
// content as Anchors. Useful for catalog field descriptions that ship
// `[label](url)` segments inside connections-metadata.json. Other
// markdown syntax is not interpreted — descriptions only carry inline
// links today and reaching for a full markdown parser is overkill.
//
// Pass any prop the underlying Mantine Text accepts (size, c, fw, ...);
// the defaults match the helper-text styling used throughout the
// Configure Role form so call sites can simply drop in this component.
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
