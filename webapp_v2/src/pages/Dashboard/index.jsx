import { useEffect, useMemo } from 'react'
import { Stack, Group, Grid, Card, Title, Center } from '@mantine/core'
import BarChart from '@/components/BarChart'
import DonutChart from '@/components/DonutChart'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useUserStore } from '@/stores/useUserStore'
import ChartCard from './components/ChartCard'
import ComingSoon from './components/ComingSoon'
import RangeFilter from './components/RangeFilter'
import StatBlock from './components/StatBlock'
import { FREE_LICENSE_MESSAGE } from './constants'
import { useDashboardStore } from './store'
import {
  buildConnectionSlices,
  buildRedactedItems,
  buildReviewBuckets,
  countReviewsToday,
  rangeLabel,
} from './utils'

// Deep green against soft coral rather than the two saturated shades this page
// started with. The muting is the point, but the pairing is not free-hand: red
// and green sit almost on top of each other for red-green colour blindness, so
// the two have to be pulled apart in lightness to stay legible.
//
// Verified with the dataviz palette validator (light surface): CVD ΔE 12.5
// (protan) — comfortably over the 8 target, where the previous green.5/red.5
// scored 7.5 and evenly-muted variants collapsed to 3.6.
//
// The soft coral sits at 2.8:1 against the page, under the 3:1 mark, which
// obliges a visible label — hence `withLegend` on the chart below. That is also
// the rule for any two-series chart: identity must never rest on colour alone.
const REVIEW_SERIES = [
  { name: 'approved', label: 'Approved', color: 'green.8' },
  { name: 'rejected', label: 'Rejected', color: 'red.3' },
]

const REDACTED_SERIES = [{ name: 'redactTotal', label: 'Redacted', color: 'indigo.6' }]

// Legacy card body heights, which are deliberately not uniform.
const CHART_HEIGHT = 300
const REDACTED_CHART_HEIGHT = 400

// Reproduces the legacy donut geometry (innerRadius 60 inside a 300px box):
// Mantine derives outerRadius = size / 2 and innerRadius = size / 2 - thickness.
const DONUT_SIZE = 240
const DONUT_THICKNESS = 60

function Dashboard() {
  const isFreeLicense = useUserStore((state) => state.isFreeLicense)

  const loading = useDashboardStore((state) => state.loading)
  const fetchAll = useDashboardStore((state) => state.fetchAll)

  const todaySessionsTotal = useDashboardStore((state) => state.todaySessionsTotal)
  const todayRedactedTotal = useDashboardStore((state) => state.todayRedactedTotal)

  const reviews = useDashboardStore((state) => state.reviews)
  const reviewsError = useDashboardStore((state) => state.reviewsError)
  const reviewRange = useDashboardStore((state) => state.reviewRange)
  const setReviewRange = useDashboardStore((state) => state.setReviewRange)

  const connections = useDashboardStore((state) => state.connections)
  const connectionsError = useDashboardStore((state) => state.connectionsError)

  const redactedItems = useDashboardStore((state) => state.redactedItems)
  const redactedRange = useDashboardStore((state) => state.redactedRange)
  const redactedRangeLabel = useDashboardStore((state) => state.redactedRangeLabel)
  const redactedError = useDashboardStore((state) => state.redactedError)
  const setRedactedRange = useDashboardStore((state) => state.setRedactedRange)

  const showLoader = useMinDelay(loading)

  useEffect(() => {
    fetchAll()
  }, [fetchAll])

  // The reviews list is fetched once and every derived view recomputed locally —
  // /reviews accepts no query parameters, so refetching on a range change could
  // not return anything different.
  const todayReviewsTotal = useMemo(
    () => (reviewsError ? null : countReviewsToday(reviews)),
    [reviews, reviewsError]
  )
  const reviewBuckets = useMemo(
    () => buildReviewBuckets(reviews, Number(reviewRange)),
    [reviews, reviewRange]
  )
  const reviewRangeLabel = useMemo(() => rangeLabel(Number(reviewRange)), [reviewRange])

  const redactedData = useMemo(() => buildRedactedItems(redactedItems), [redactedItems])
  const connectionSlices = useMemo(() => buildConnectionSlices(connections), [connections])

  if (showLoader) {
    return <PageLoader h={400} />
  }

  return (
    <Stack gap="xxlAlt">
      <Stack gap="lgAlt">
        <Title order={1}>Dashboard</Title>
        {isFreeLicense && <FreeLicenseCallout message={FREE_LICENSE_MESSAGE} />}
      </Stack>

      <Card withBorder p="lgAlt">
        <Stack gap="lgAlt">
          <Title order={4}>{"Today's overview"}</Title>
          <Group justify="space-between" align="flex-start" gap="lgAlt" wrap="wrap">
            <StatBlock
              title="Sessions"
              caption="reviewed and safely executed"
              value={todaySessionsTotal}
            />
            <StatBlock
              title="Reviews"
              caption="sent via safe channels"
              value={todayReviewsTotal}
            />
            <StatBlock
              title="Redacted Data"
              caption="protected with Live Data Masking"
              value={todayRedactedTotal}
            />
          </Group>
        </Stack>
      </Card>

      <Grid gutter="lgAlt">
        <Grid.Col span={{ base: 12, md: 6 }}>
          <ChartCard title="Sessions" minH={CHART_HEIGHT}>
            <ComingSoon />
          </ChartCard>
        </Grid.Col>
        <Grid.Col span={{ base: 12, md: 6 }}>
          <ChartCard
            title="Reviews"
            subtitle={reviewRangeLabel}
            minH={CHART_HEIGHT}
            error={reviewsError}
            empty={reviewBuckets.length === 0}
            control={
              <RangeFilter
                value={reviewRange}
                onChange={setReviewRange}
                isFreeLicense={isFreeLicense}
              />
            }
          >
            <BarChart
              h={CHART_HEIGHT}
              data={reviewBuckets}
              dataKey="label"
              series={REVIEW_SERIES}
              withXAxis={false}
              withYAxis={false}
              withLegend
              barProps={{ radius: 4 }}
            />
          </ChartCard>
        </Grid.Col>
      </Grid>

      <ChartCard
        title="Redacted Data"
        subtitle={redactedRangeLabel}
        minH={REDACTED_CHART_HEIGHT}
        error={redactedError}
        empty={redactedData.length === 0}
        control={
          <RangeFilter
            value={redactedRange}
            onChange={setRedactedRange}
            isFreeLicense={isFreeLicense}
          />
        }
      >
        <BarChart
          h={REDACTED_CHART_HEIGHT}
          data={redactedData}
          dataKey="infoType"
          series={REDACTED_SERIES}
          withYAxis={false}
          barProps={{ radius: 4 }}
        />
      </ChartCard>

      <Grid gutter="lgAlt">
        <Grid.Col span={{ base: 12, md: 6 }}>
          <ChartCard title="Runbooks" minH={CHART_HEIGHT}>
            <ComingSoon />
          </ChartCard>
        </Grid.Col>
        <Grid.Col span={{ base: 12, md: 6 }}>
          <ChartCard
            title="Resource Roles"
            minH={CHART_HEIGHT}
            error={connectionsError}
            empty={connectionSlices.length === 0}
          >
            <Center h={CHART_HEIGHT}>
              <DonutChart
                data={connectionSlices}
                size={DONUT_SIZE}
                thickness={DONUT_THICKNESS}
                strokeWidth={5}
              />
            </Center>
          </ChartCard>
        </Grid.Col>
      </Grid>
    </Stack>
  )
}

export default Dashboard
