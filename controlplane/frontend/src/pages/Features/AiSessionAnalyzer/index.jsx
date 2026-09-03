import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Group, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import PageLoader from '@/components/PageLoader'
import Tabs from '@/components/Tabs'
import { useMinDelay } from '@/hooks/useMinDelay'
import FullBleed from '@/layout/FullBleed'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { LICENSE_FEATURE_LABELS, licenseRequiredMessage, licenseState } from '@/utils/license'
import { useAiSessionAnalyzerStore } from './store'
import RulesTab from './RulesTab'
import ConfigureTab from './ConfigureTab'
import AiSessionAnalyzerPromotion from './components/AiSessionAnalyzerPromotion'

// The CLJS activation journey writes the same key, so both stacks agree on
// what "seen" means.
const PROMOTION_SEEN_STORAGE_KEY = 'ai-session-analyzer-promotion-seen'

export default function AiSessionAnalyzer() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()

  // Creating a rule needs a valid Enterprise license (hard zero on the client).
  // Editing and deleting the rules that exist stay open in every state.
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const licenseInfo = useUserStore((s) => s.licenseInfo)
  const openLicenseModal = useUIStore((s) => s.openLicenseModal)

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

  const createBlocked = isFreeLicense
  const licenseMessage = licenseRequiredMessage(
    LICENSE_FEATURE_LABELS['ai-session-analyzer'],
    licenseState(licenseInfo),
  )

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>AI Session Analyzer</Title>
          <Text size="md" c="dimmed">
            Monitor terminal sessions and resource usage in real time.
          </Text>
        </Stack>
        {list.length > 0 && (
          <Button
            onClick={() => navigate('/features/ai-session-analyzer/rules/new')}
            disabled={createBlocked}
          >
            Create new rule
          </Button>
        )}
      </Group>

      {createBlocked && <FreeLicenseCallout message={licenseMessage} />}

      <Tabs value={tab} onChange={setTab}>
        <Tabs.List aria-label="AI Session Analyzer tabs">
          <Tabs.Tab value="rules">Rules</Tabs.Tab>
          <Tabs.Tab value="configure">Configure</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="rules" pt="md">
          <RulesTab
            providerConfigured={Boolean(provider)}
            createBlocked={createBlocked}
            onAddLicense={openLicenseModal}
            onGoConfigure={() => setTab('configure')}
          />
        </Tabs.Panel>
        <Tabs.Panel value="configure" pt="xl">
          <ConfigureTab onSaved={() => setTab('rules')} />
        </Tabs.Panel>
      </Tabs>
    </Stack>
  )
}
