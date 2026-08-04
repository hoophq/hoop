import { DonutChart as MantineDonutChart } from '@mantine/charts'

/**
 * Donut chart wrapper (recharts via @mantine/charts).
 *
 * `data` is `[{ name, value, color }]` — colors are per slice, so pull them from
 * CHART_SERIES_COLORS in `@/theme` and cycle with `i % length`.
 *
 * Defaults:
 *   - withLabels={false}           Labels go in the tooltip, not on the arcs.
 *   - tooltipDataSource="segment"  Mantine defaults to "all", which lists every
 *                                  slice at once; we show only the hovered one.
 *
 * Mantine derives radii from size/thickness: outerRadius = size / 2 and
 * innerRadius = size / 2 - thickness.
 *
 * Usage:
 *   <DonutChart size={240} thickness={60} strokeWidth={5} data={slices} />
 */
export default function DonutChart({
  withLabels = false,
  tooltipDataSource = 'segment',
  ...props
}) {
  return (
    <MantineDonutChart
      withLabels={withLabels}
      tooltipDataSource={tooltipDataSource}
      {...props}
    />
  )
}
