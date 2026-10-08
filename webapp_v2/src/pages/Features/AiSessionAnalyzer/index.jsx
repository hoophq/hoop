import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import NewRuleButton from '@/components/NewRuleButton'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import FullBleed from '@/layout/FullBleed'
import { useNewRuleTraffics } from '@/hooks/useNewRuleTraffics'
import { useRuleTraffics } from '@/modes'
import { useUserStore } from '@/stores/useUserStore'
import { TRAFFIC_AGENT, newRulePath } from '@/utils/ruleTraffic'
import { useAiSessionAnalyzerStore } from './store'
import { FREE_LICENSE_LIMIT_MESSAGE } from './helpers'
import RulesTab from './RulesTab'
import AiSessionAnalyzerPromotion from './components/AiSessionAnalyzerPromotion'

// The CLJS activation journey writes the same key, so both stacks agree on
// what "seen" means.
const PROMOTION_SEEN_STORAGE_KEY = 'ai-session-analyzer-promotion-seen'

// ConfigureTab and orgWideProvider come from Router.jsx through <ByProduct>, so
// this page serves both products without reading the mode.
//
// orgWideProvider is false where the provider is a property of each sidecar
// rather than of the organization. The empty state reads it: pushing a control
// plane admin at "Configure AI Session Analyzer" would send them to a tab that
// has nothing to save.
export default function AiSessionAnalyzer({ ConfigureTab, orgWideProvider = true }) {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const traffics = useRuleTraffics()
  const mixed = traffics.length > 1
  const newTraffics = useNewRuleTraffics()

  const list = useAiSessionAnalyzerStore((s) => s.list)
  const listStatus = useAiSessionAnalyzerStore((s) => s.listStatus)
  const provider = useAiSessionAnalyzerStore((s) => s.provider)
  const providerStatus = useAiSessionAnalyzerStore((s) => s.providerStatus)
  const fetchList = useAiSessionAnalyzerStore((s) => s.fetchList)
  const fetchProvider = useAiSessionAnalyzerStore((s) => s.fetchProvider)

  const tab = searchParams.get('tab') === 'configure' ? 'configure' : 'rules'
  const setTab = (value) =>
    setSearchParams(value === 'configure' ? { tab: value } : {}, { replace: true })

  const [promotionSeen, setPromotionSeen] = useState(() =>
    Boolean(localStorage.getItem(PROMOTION_SEEN_STORAGE_KEY)),
  )
  const markPromotionSeen = () => {
    localStorage.setItem(PROMOTION_SEEN_STORAGE_KEY, 'true')
    setPromotionSeen(true)
  }

  useEffect(() => {
    fetchList()
    fetchProvider()
  }, [fetchList, fetchProvider])

  const loading =
    listStatus === 'idle' ||
    listStatus === 'loading' ||
    providerStatus === 'idle' ||
    providerStatus === 'loading'
  const showLoader = useMinDelay(loading, 500)

  if (showLoader) {
    return <PageLoader h={300} />
  }

  // A failed load leaves the list empty, which would otherwise fall through to
  // the empty state and tell an admin they have no rules configured.
  if (listStatus === 'error' || providerStatus === 'error') {
    return <PageLoader error h={300} message="Failed to load AI Session Analyzer." />
  }

  // The promotion replaces the whole page, not just the empty state — an admin
  // sees it even with rules already configured.
  if (!promotionSeen) {
    return (
      <FullBleed>
        <AiSessionAnalyzerPromotion
          onConfigure={() => {
            markPromotionSeen()
            setTab('configure')
          }}
        />
      </FullBleed>
    )
  }

  // Free-plan parity with Guardrails and Live Data Masking: one rule per org.
  const atFreeLimit = isFreeLicense && list.length >= 1

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>AI Session Analyzer</Title>
          <Text size="md" c="dimmed">
            Monitor terminal sessions and resource usage in real time.
          </Text>
        </Stack>
        {tab === 'rules' && (list.length > 0 || mixed) && (
          <NewRuleButton
            traffics={newTraffics}
            onCreate={(kind) => navigate(newRulePath('/features/ai-session-analyzer/rules/new', kind))}
            disabled={atFreeLimit}
            blocked={
              orgWideProvider && !provider
                ? { [TRAFFIC_AGENT]: 'Configure the provider first' }
                : undefined
            }
          >
            Create new rule
          </NewRuleButton>
        )}
      </Group>

      {atFreeLimit && (
        <FreeLicenseCallout message={FREE_LICENSE_LIMIT_MESSAGE} variant="limit" />
      )}

      {tab === 'configure' ? (
        <Box>
          {mixed && (
            <Text size="sm" c="dimmed" mb="lg">
              This provider analyzes agent sessions. Each sidecar uses the provider in its own
              configuration.
            </Text>
          )}
          <ConfigureTab onSaved={() => setTab('rules')} />
        </Box>
      ) : (
        <RulesTab
          providerConfigured={!orgWideProvider || Boolean(provider)}
          onGoConfigure={() => setTab('configure')}
          newTraffics={newTraffics}
        />
      )}
    </Stack>
  )
}
