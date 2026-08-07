import { BarChart as MantineBarChart } from '@mantine/charts'

/**
 * Bar chart wrapper (recharts via @mantine/charts).
 *
 * Only carries app-wide defaults; every chart-specific decision (series, axes,
 * height, bar radius) stays at the call site. Two consumers can need opposite
 * configurations — encoding either one here would be wrong by the next chart.
 *
 * Defaults:
 *   - gridAxis="none"  Mantine defaults to "x"; our charts are grid-less.
 *   - withLegend={false}
 *   - tickLine="none"
 *   - valueFormatter    Thousands separators in the tooltip and on the y-axis.
 *
 * Usage:
 *   <BarChart
 *     h={300}
 *     data={data}
 *     dataKey="label"
 *     withXAxis={false}
 *     withYAxis={false}
 *     barProps={{ radius: 4 }}
 *     series={[{ name: 'approved', label: 'Approved', color: 'green.5' }]}
 *   />
 */
export default function BarChart({
  gridAxis = 'none',
  withLegend = false,
  tickLine = 'none',
  valueFormatter = (value) => value.toLocaleString(),
  ...props
}) {
  return (
    <MantineBarChart
      gridAxis={gridAxis}
      withLegend={withLegend}
      tickLine={tickLine}
      valueFormatter={valueFormatter}
      {...props}
    />
  )
}
