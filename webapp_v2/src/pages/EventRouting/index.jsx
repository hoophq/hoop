import { useEffect } from "react"
import { Navigate, useNavigate } from "react-router-dom"
import { Group, Stack, Text, Title } from "@mantine/core"
import { Plus } from "lucide-react"
import Badge from "@/components/Badge"
import Button from "@/components/Button"
import Tabs from "@/components/Tabs"
import PageLoader from "@/components/PageLoader"
import { useUserStore } from "@/stores/useUserStore"
import { useEventRoutingStore } from "./store"
import SubscriptionsTab from "./components/SubscriptionsTab"
import EventCatalogTab from "./components/EventCatalogTab"
import EventDetailModal from "./components/EventDetailModal"

const FEATURE_FLAG = "experimental.event_routing"

export default function EventRouting() {
  const navigate = useNavigate()
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const flagEnabled = isFeatureFlagEnabled(FEATURE_FLAG)

  const { subscriptions, catalog, activeTab, setActiveTab, fetchAll } =
    useEventRoutingStore()

  const loading =
    subscriptions.status === "loading" || catalog.status === "loading"

  useEffect(() => {
    if (flagEnabled) fetchAll()
  }, [flagEnabled, fetchAll])

  if (!flagEnabled) {
    return <Navigate to="/" replace />
  }

  if (subscriptions.status === "error") {
    return <Text c="red">{subscriptions.error}</Text>
  }

  const goToCreate = () => navigate("/features/event-routing/new")

  return (
    <>
      <EventDetailModal />

      <Stack gap="xl">
        <Group justify="space-between" align="flex-start">
          <Stack gap="sm">
            <Group gap="sm" align="center">
              <Title order={1}>Event Routing</Title>
              <Badge color="indigo" size="sm">
                BETA
              </Badge>
            </Group>
            <Text size="md" c="dimmed">
              Route platform events to runbooks. Each subscription becomes an
              audited automation surface.
            </Text>
          </Stack>
          <Button leftSection={<Plus size={16} />} onClick={goToCreate}>
            Create subscription
          </Button>
        </Group>

        {loading ? (
          <PageLoader h={400} />
        ) : (
          <Tabs value={activeTab} onChange={setActiveTab}>
            <Tabs.List aria-label="Event Routing tabs">
              <Tabs.Tab value="subscriptions">Subscriptions</Tabs.Tab>
              <Tabs.Tab value="catalog">Event catalog</Tabs.Tab>
            </Tabs.List>

            <Tabs.Panel value="subscriptions" pt="md">
              <SubscriptionsTab onOpenCreate={goToCreate} />
            </Tabs.Panel>
            <Tabs.Panel value="catalog" pt="md">
              <EventCatalogTab />
            </Tabs.Panel>
          </Tabs>
        )}
      </Stack>
    </>
  )
}
