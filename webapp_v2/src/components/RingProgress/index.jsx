import { RingProgress as MantineRingProgress, Text } from '@mantine/core'

/**
 * Compact progress ring with a percentage label, sized for inline/sidebar use.
 * `value` is 0–100. Pass `label` to override the default percentage text, or
 * any Mantine RingProgress prop to customize further.
 *
 * The arc color defaults to indigo.5 (#3e63dd) — the Figma "main accent" —
 * not the theme primaryShade.
 */
export default function RingProgress({
  value,
  size = 32,
  thickness = 3,
  color = 'indigo.5',
  rootColor = 'gray.2',
  label,
  ...props
}) {
  return (
    <MantineRingProgress
      size={size}
      thickness={thickness}
      rootColor={rootColor}
      sections={[{ value, color }]}
      label={
        label ?? (
          <Text fz={9} ta="center" lh={1}>
            {`${Math.round(value)}%`}
          </Text>
        )
      }
      {...props}
    />
  )
}
