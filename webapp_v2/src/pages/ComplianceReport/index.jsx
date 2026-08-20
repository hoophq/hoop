import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Center,
  Divider,
  Grid,
  Group,
  Progress,
  RingProgress,
  SimpleGrid,
  Skeleton,
  Stack,
  Tabs,
  Text,
  ThemeIcon,
  Title,
  UnstyledButton,
} from '@mantine/core'
import { Carousel } from '@mantine/carousel'
import { AlertCircle, ChevronLeft, ChevronRight, Share, SquareArrowOutUpRight } from 'lucide-react'
import { reportsService } from '@/services/reports'
import { showSnackbar } from '@/utils/snackbar'
import FrameworkPanel from './components/FrameworkPanel'
import { StatusIndicator } from './components/ControlBits'
import {
  CATEGORY_COLORS,
  CATEGORY_ICONS,
  CATEGORY_ORDER,
  CATEGORY_SUBTITLES,
  LEVEL_META,
  catalogDocsUrl,
} from './constants'
import './print.css'

// Mirrors the final layout so data resolving does not shift content (spec:
// skeleton state). Kept coarse on purpose — placeholders, not a wireframe.
function ReportSkeleton() {
  return (
    <Stack gap="xxlAlt">
      <Group justify="space-between">
        <Stack gap="xs">
          <Skeleton h={32} w={280} />
          <Skeleton h={18} w={420} />
        </Stack>
        <Skeleton h={36} w={110} />
      </Group>
      <Grid gutter="lgAlt">
        <Grid.Col span={{ base: 12, md: 4 }}>
          <Skeleton h={280} radius="md" />
        </Grid.Col>
        <Grid.Col span={{ base: 12, md: 8 }}>
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="lgAlt">
            {[...Array(6)].map((_, i) => (
              <Skeleton key={i} h={128} radius="md" />
            ))}
          </SimpleGrid>
        </Grid.Col>
      </Grid>
      <Skeleton h={220} radius="md" />
      <Skeleton h={400} radius="md" />
    </Stack>
  )
}

function ScoreCard({ overall }) {
  const level = LEVEL_META[overall.level] ?? LEVEL_META.low
  return (
    <Card withBorder p="md" h="100%">
      <Stack gap="sm" h="100%">
        <Text size="lg" fw={600}>Compliance Score</Text>
        <Center style={{ flex: 1 }}>
          <RingProgress
            size={220}
            thickness={14}
            roundCaps
            rootColor="gray.1"
            sections={[{ value: overall.score / 10, color: level.color }]}
            label={
              <Stack gap={6} align="center">
                <Title order={1} fz={46} lh={1}>
                  {overall.score}
                </Title>
                <Divider w={72} color="gray.3" />
                <Text fz="23" c="dark.4" lh={1}>
                  1000
                </Text>
              </Stack>
            }
          />
        </Center>
        <Text size="sm" c="dimmed" ta="center">
          Based on active controls across observed security frameworks detailed below
        </Text>
      </Stack>
    </Card>
  )
}

function CategoryCard({ category }) {
  // A category whose checks are all non-applicable has total 0 — that is
  // "nothing to assess", not failure: render it muted.
  const empty = category.total === 0
  const pct = empty ? 0 : Math.round((100 * category.compliant) / category.total)
  const color = empty ? 'gray' : CATEGORY_COLORS[category.id] ?? 'gray'
  const Icon = CATEGORY_ICONS[category.id]
  return (
    <Card withBorder p="md">
      <Stack gap={24} justify="space-between" h="100%">
        <Group justify="space-between" align="center" wrap="nowrap">
          <Text size="sm" fw={600}>{category.title}</Text>
          {Icon && (
            <ThemeIcon variant="light" color={color} size="lg" radius="md">
              <Icon size={18} />
            </ThemeIcon>
          )}
        </Group>
        <Stack gap={12}>
          <Group gap={0} align="baseline">
            <Title fz={28} lh="36px" order={2}>{category.compliant}</Title>
            <Text fz="lg" lh="26px" fw={700}>
              /{category.total}
            </Text>
          </Group>
          <Progress value={pct} color={color} size={4} radius="xl" />
          <Text fz="xs" c="dimmed" lineClamp={1}>
            {CATEGORY_SUBTITLES[category.id] ?? ''}
          </Text>
        </Stack>
      </Stack>
    </Card>
  )
}

// One remediation card. Whole card navigates when the action is actionable
// in-product; carousel slides and the print fallback share it. The category
// tag uses the backend display title (falls back to a prettified id).
function ActionRequiredItem({ item, categoryTitle, onNavigate }) {
  const clickable = item.action?.type === 'app' || item.action?.type === 'docs'
  const card = (
    <Card withBorder p="md" h="100%">
      <Stack gap="xs" justify="space-between" h="100%">
        <Group align="flex-start" justify="space-between" wrap="nowrap" gap="sm">
          <Group align="flex-start" wrap="nowrap" gap="sm">
            <StatusIndicator status={item.status} message={item.message} evidence={item.evidence} />
            <Stack gap={2}>
              <Text size="sm" fw={500}>
                {item.title}
              </Text>
              <Text size="sm" c="dimmed" lineClamp={3}>
                {item.message}
              </Text>
            </Stack>
          </Group>
          {clickable && (
            <SquareArrowOutUpRight
              size={16}
              color="var(--mantine-color-dimmed)"
              style={{ flexShrink: 0 }}
            />
          )}
        </Group>
        <Group>
          <Badge
            variant="light"
            color={CATEGORY_COLORS[item.category] ?? 'gray'}
            size="sm"
            radius="sm"
            tt="none"
          >
            {categoryTitle ?? item.category.replaceAll('_', ' ')}
          </Badge>
        </Group>
      </Stack>
    </Card>
  )
  if (!clickable) return card
  return (
    <UnstyledButton onClick={() => onNavigate(item.action)} h="100%" w="100%">
      {card}
    </UnstyledButton>
  )
}

function ComplianceReport() {
  const navigate = useNavigate()
  const [report, setReport] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [activeTab, setActiveTab] = useState(null)
  const [printing, setPrinting] = useState(false)
  const [embla, setEmbla] = useState(null)
  const [canScroll, setCanScroll] = useState({ prev: false, next: false })

  // load() never touches state synchronously — the initial state already is
  // loading/no-error, so the mount effect stays free of cascading renders.
  const load = useCallback(() => {
    return reportsService
      .getComplianceReport()
      .then((data) => {
        setReport(data)
        setActiveTab((current) => current ?? data.frameworks?.[0]?.id ?? null)
      })
      .catch(() => {
        setError(true)
        showSnackbar({ level: 'error', text: 'Failed to load the compliance report' })
      })
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const retry = () => {
    setLoading(true)
    setError(false)
    load()
  }

  // Export = print flow: render every framework stacked (tabs removed, spec's
  // "single continuous layout"), print, restore.
  useEffect(() => {
    if (!printing) return undefined
    document.body.classList.add('compliance-printing')
    const done = () => setPrinting(false)
    window.addEventListener('afterprint', done)
    const timer = setTimeout(() => window.print(), 100)
    return () => {
      clearTimeout(timer)
      window.removeEventListener('afterprint', done)
      document.body.classList.remove('compliance-printing')
    }
  }, [printing])

  // Header prev/next drive embla directly (built-in overlay controls are
  // disabled). Edge state comes from embla events; the deferred initial sync
  // keeps setState out of the synchronous effect body.
  useEffect(() => {
    if (!embla) return undefined
    const update = () => {
      setCanScroll({ prev: embla.canScrollPrev(), next: embla.canScrollNext() })
    }
    const timer = setTimeout(update, 0)
    embla.on('select', update)
    embla.on('reInit', update)
    return () => {
      clearTimeout(timer)
      embla.off('select', update)
      embla.off('reInit', update)
    }
  }, [embla])

  const handleAction = (action) => {
    if (action.type === 'app') navigate(action.target)
    else if (action.type === 'docs') window.open(catalogDocsUrl(action.target), '_blank', 'noopener')
  }

  if (loading) return <ReportSkeleton />

  if (error || !report) {
    return (
      <Stack gap="lgAlt">
        <Title order={1}>Compliance Report</Title>
        <Alert icon={<AlertCircle size={16} />} color="red" title="Report unavailable">
          <Stack gap="sm" align="flex-start">
            <Text size="sm">The compliance report could not be loaded.</Text>
            <Button variant="light" size="xs" onClick={retry}>
              Try again
            </Button>
          </Stack>
        </Alert>
      </Stack>
    )
  }

  const { overall, categories = [], action_required: actionRequired = [], frameworks = [] } = report
  // Backend ships display titles per category; look them up instead of
  // re-deriving from ids.
  const categoryTitles = Object.fromEntries(categories.map((c) => [c.id, c.title]))
  // Card display order follows the design, not the API order.
  const orderedCategories = [...categories].sort((a, b) => {
    const rank = (c) => {
      const i = CATEGORY_ORDER.indexOf(c.id)
      return i === -1 ? CATEGORY_ORDER.length : i
    }
    return rank(a) - rank(b)
  })

  return (
    <Stack gap="xxlAlt" id="compliance-report-root">
      <Group justify="space-between" align="flex-start">
        <Stack gap="xs">
          <Title order={1}>Compliance Report</Title>
          <Text size="lg" c="dimmed">
            Framework-aligned view of your security posture across SOC 2, GDPR, PCI DSS and HIPAA.
          </Text>
        </Stack>
        <Button
          leftSection={<Share size={16} />}
          onClick={() => setPrinting(true)}
        >
          Export
        </Button>
      </Group>

      {/* The overview row stretches both columns to the same height: the
          score card fills the left cell, and the category grid divides the
          right cell into two equal rows (gridAutoRows: 1fr). */}
      <Grid gutter="md">
        <Grid.Col span={{ base: 12, md: 4 }}>
          <ScoreCard overall={overall} />
        </Grid.Col>
        <Grid.Col span={{ base: 12, md: 8 }}>
          <SimpleGrid
            cols={{ base: 1, sm: 2, lg: 3 }}
            spacing="md"
            h="100%"
            style={{ gridAutoRows: '1fr' }}
          >
            {orderedCategories.map((category) => (
              <CategoryCard key={category.id} category={category} />
            ))}
          </SimpleGrid>
        </Grid.Col>
      </Grid>

      {actionRequired.length > 0 && (
        <Stack gap="xs">
          <Group justify="space-between" align="center">
            <Title order={4}>Action Required ({actionRequired.length})</Title>
            {!printing && (
              <Group gap="xs">
                <ActionIcon
                  variant="default"
                  size="xs"
                  radius="sm"
                  aria-label="Previous actions"
                  disabled={!canScroll.prev}
                  onClick={() => embla?.scrollPrev()}
                >
                  <ChevronLeft size={16} />
                </ActionIcon>
                <ActionIcon
                  variant="default"
                  size="xs"
                  radius="sm"
                  aria-label="Next actions"
                  disabled={!canScroll.next}
                  onClick={() => embla?.scrollNext()}
                >
                  <ChevronRight size={16} />
                </ActionIcon>
              </Group>
            )}
          </Group>
          {printing ? (
            // Horizontal scrollers do not print; the export stacks the cards.
            <Stack gap="sm">
              {actionRequired.map((item) => (
                <ActionRequiredItem
                  key={item.id}
                  item={item}
                  categoryTitle={categoryTitles[item.category]}
                  onNavigate={handleAction}
                />
              ))}
            </Stack>
          ) : (
            <Carousel
              withControls={false}
              getEmblaApi={setEmbla}
              slideSize={{ base: '100%', sm: '50%', lg: '33.333333%' }}
              slideGap="md"
              emblaOptions={{ align: 'start', slidesToScroll: 1, containScroll: 'trimSnaps' }}
              styles={{ slide: { display: 'flex' } }}
            >
              {actionRequired.map((item) => (
                <Carousel.Slide key={item.id}>
                  <ActionRequiredItem
                    item={item}
                    categoryTitle={categoryTitles[item.category]}
                    onNavigate={handleAction}
                  />
                </Carousel.Slide>
              ))}
            </Carousel>
          )}
        </Stack>
      )}

      {printing ? (
        <Stack gap="xxlAlt">
          {frameworks.map((framework) => (
            <Stack key={framework.id} gap="lgAlt">
              <Title order={2}>{framework.name}</Title>
              <FrameworkPanel framework={framework} showDetails />
            </Stack>
          ))}
        </Stack>
      ) : (
        <Tabs value={activeTab} onChange={setActiveTab} keepMounted={false}>
          <Tabs.List mb="lgAlt">
            {frameworks.map((framework) => (
              <Tabs.Tab key={framework.id} value={framework.id}>
                {framework.name}
              </Tabs.Tab>
            ))}
          </Tabs.List>
          {frameworks.map((framework) => (
            <Tabs.Panel key={framework.id} value={framework.id}>
              <FrameworkPanel framework={framework} />
            </Tabs.Panel>
          ))}
        </Tabs>
      )}
    </Stack>
  )
}

export default ComplianceReport
