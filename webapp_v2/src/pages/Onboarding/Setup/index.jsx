import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Center, Stack, Text, Title } from '@mantine/core'
import { BrainCog } from 'lucide-react'
import ActionCard from '@/components/ActionCard'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import OnboardingLayout, { OnboardingLogo } from '@/layout/OnboardingLayout'
import ResourceCatalog from '@/pages/Resources/Catalog'
import { connectionsService } from '@/services/connections'
import { protectionProfilesService } from '@/services/protectionProfiles'
import { useAgentStore } from '@/stores/useAgentStore'
import { useBridgeStore } from '@/stores/useBridgeStore'
import { showSnackbar } from '@/utils/snackbar'
import { DEMO_NAME, DEMO_QUERY, RESOURCE_SETUP_PATH, TERMINAL_DRAFT_KEY, demoConnection } from '../onboarding'

function SetUpAgent() {
  const navigate = useNavigate()
  return (
    <OnboardingLayout logout>
      <Center>
        <Stack gap="xl" w="100%" maw={600} align="center">
          <OnboardingLogo />
          <Stack gap="sm" align="center">
            <Title order={1} ta="center">
              Set up an Agent
            </Title>
            <Text size="md" c="dimmed" ta="center">
              An Agent connects Hoop to your Resources. You can run it on your local machine to start.
            </Text>
          </Stack>
          <ActionCard
            icon={BrainCog}
            title="Set up an Agent"
            description="Create an Agent to start using Hoop"
            onClick={() => navigate('/onboarding/setup/agent')}
          />
        </Stack>
      </Center>
    </OnboardingLayout>
  )
}

// /onboarding/setup: the Agent comes first, then the catalog.
export default function OnboardingSetup() {
  const navigate = useNavigate()
  const agents = useAgentStore((s) => s.agents)
  const loading = useAgentStore((s) => s.loading)
  const fetchAgents = useAgentStore((s) => s.fetchAgents)
  // The store starts with no Agents and not loading: wait for this page's own
  // fetch. Its error is kept whole, for the gateway's message.
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState(null)
  const [creatingDemo, setCreatingDemo] = useState(false)

  const load = useCallback(
    () =>
      fetchAgents().then((err) => {
        setLoadError(err)
        setLoaded(true)
      }),
    [fetchAgents],
  )

  useEffect(() => {
    load()
  }, [load])

  const createDemo = async () => {
    setCreatingDemo(true)
    try {
      const { attribute_name: profileAttribute } = await protectionProfilesService.get()
      await connectionsService.create(demoConnection({ agentId: agents[0].id, profileAttribute }))
    } catch (err) {
      setCreatingDemo(false)
      showSnackbar({
        level: 'error',
        text: 'Failed to create the demo Resource',
        description: err.response?.data?.message,
      })
      return
    }
    try {
      localStorage.setItem(TERMINAL_DRAFT_KEY, JSON.stringify({ date: Date.now(), code: DEMO_QUERY }))
    } catch {
      // Storage can be blocked: the terminal opens empty.
    }
    showSnackbar({ level: 'success', text: 'Demo Resource created.' })
    useBridgeStore.getState().clearDatabaseSchema()
    navigate('/client?role=' + encodeURIComponent(DEMO_NAME))
    useBridgeStore.getState().syncPrimaryConnectionFromUrl()
  }

  const onAction = (entry) => {
    if (entry.id === 'aws-discovery') navigate('/onboarding/resource-providers')
    else if (!creatingDemo) createDemo()
  }

  if (!loaded || loading) return <PageLoader overlay />
  if (loadError) {
    return (
      <OnboardingLayout logout>
        <EmptyState
          title="Could not load your Agents"
          description={loadError.response?.data?.message}
          action={{ label: 'Try again', onClick: load }}
        />
      </OnboardingLayout>
    )
  }
  if (agents.length === 0) return <SetUpAgent />
  return (
    <OnboardingLayout>
      {creatingDemo && <PageLoader overlay message="Creating the demo Resource" />}
      <ResourceCatalog onboarding onAction={onAction} setupPath={RESOURCE_SETUP_PATH} />
    </OnboardingLayout>
  )
}
