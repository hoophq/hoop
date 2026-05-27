import { useEffect, useState } from "react"
import { Navigate, useNavigate, useParams } from "react-router-dom"
import { Box, Card, Group, Stack, Text, Title } from "@mantine/core"
import { useDisclosure } from "@mantine/hooks"
import { notifications } from "@mantine/notifications"
import {
  ArrowLeft,
  ArrowRight,
  History,
  Pause,
  Pencil,
  Play,
  RotateCcw,
  BookUp2,
  Trash2,
  TriangleAlert,
} from "lucide-react"
import ActionIcon from "@/components/ActionIcon"
import Alert from "@/components/Alert"
import Button from "@/components/Button"
import Code from "@/components/Code"
import Modal from "@/components/Modal"
import PageLoader from "@/components/PageLoader"
import Tooltip from "@/components/Tooltip"
import { useUserStore } from "@/stores/useUserStore"
import { useEventRoutingStore } from "../store"
import StatusBadge from "../components/StatusBadge"
import DispatchBadge from "../components/DispatchBadge"
import ReplayDispatchModal from "../components/ReplayDispatchModal"

const FEATURE_FLAG = "experimental.event_routing"

function SectionHeader({ title, subtitle }) {
  return (
    <Stack gap={2}>
      <Title order={4}>{title}</Title>
      {subtitle && (
        <Text size="xs" c="dimmed">
          {subtitle}
        </Text>
      )}
    </Stack>
  )
}

function TargetRunbookCard({ sub }) {
  return (
    <Card padding="sm" withBorder>
      <Group justify="space-between" align="center" wrap="nowrap">
        <Group gap="md" align="center" wrap="nowrap">
          <Box
            w={36}
            h={36}
            bg="gray.2"
            style={{
              borderRadius: "var(--mantine-radius-sm)",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <BookUp2 size={16} color="var(--mantine-color-gray-9)" />
          </Box>
          <Stack gap={2}>
            <Text size="sm" fw={500}>
              {sub.runbookFile || "—"}
            </Text>
            <Group gap={4}>
              <Text size="xs" c="dimmed">
                Repository:
              </Text>
              <Code>{sub.runbookRepository || "—"}</Code>
            </Group>
            <Group gap={4}>
              <Text size="xs" c="dimmed">
                Resource role:
              </Text>
              <Text size="xs">{sub.connectionName || "—"}</Text>
            </Group>
          </Stack>
        </Group>
      </Group>
    </Card>
  )
}

function EventChip({ name }) {
  return (
    <Code
      bg="indigo.1"
      c="indigo.9"
      px="sm"
      py={5}
      style={{ border: "1px solid var(--mantine-color-indigo-3)" }}
    >
      {name}
    </Code>
  )
}

function MappingTable({ mapping }) {
  const entries = Object.entries(mapping || {})
  return (
    <Card padding={0} withBorder>
      <Stack gap={0}>
        <Group
          px="md"
          py="sm"
          wrap="nowrap"
          gap="md"
          bg="gray.0"
          style={{
            borderBottom: "1px solid var(--mantine-color-default-border)",
          }}
        >
          <Text
            size="xs"
            fw={600}
            c="dimmed"
            tt="uppercase"
            style={{ width: "40%" }}
          >
            Runbook parameter
          </Text>
          <Box w={16} />
          <Text
            size="xs"
            fw={600}
            c="dimmed"
            tt="uppercase"
            style={{ flex: 1 }}
          >
            Event payload field
          </Text>
        </Group>
        {entries.length === 0 ? (
          <Text size="xs" c="dimmed" p="md">
            No parameter mapping configured.
          </Text>
        ) : (
          entries.map(([param, source]) => (
            <Group
              key={param}
              px="md"
              py="sm"
              wrap="nowrap"
              gap="md"
              style={{
                borderBottom: "1px solid var(--mantine-color-default-border)",
              }}
            >
              <Box style={{ width: "40%" }}>
                <Code>{param}</Code>
              </Box>
              <Box w={16} style={{ textAlign: "center" }}>
                <ArrowRight size={14} color="var(--mantine-color-gray-6)" />
              </Box>
              <Box style={{ flex: 1, minWidth: 0 }}>
                <Code>{source}</Code>
              </Box>
            </Group>
          ))
        )}
      </Stack>
    </Card>
  )
}

function DispatchHistory({ subId }) {
  const dispatchState = useEventRoutingStore((s) => s.dispatches[subId])
  const setReplayTarget = useEventRoutingStore((s) => s._setReplayTarget)
  const status = dispatchState?.status || "idle"
  const data = dispatchState?.data || []

  if (status === "loading") {
    return (
      <Text size="xs" c="dimmed" p="md">
        Loading dispatches…
      </Text>
    )
  }
  if (status === "error") {
    return (
      <Text size="xs" c="red" p="md">
        Failed to load dispatches.
      </Text>
    )
  }
  if (data.length === 0) {
    return (
      <Stack align="center" gap="xs" py="xl">
        <History
          size={28}
          strokeWidth={1.5}
          color="var(--mantine-color-gray-6)"
        />
        <Text size="sm" fw={500}>
          No dispatches yet
        </Text>
        <Text size="xs" c="dimmed">
          Matching events will show up here in real time.
        </Text>
      </Stack>
    )
  }
  return (
    <Stack gap={0}>
      <Group
        px="sm"
        py="xs"
        style={{
          borderBottom: "1px solid var(--mantine-color-default-border)",
        }}
        wrap="nowrap"
      >
        <Text size="xs" fw={600} c="dimmed" w={170}>
          Timestamp
        </Text>
        <Text size="xs" fw={600} c="dimmed" style={{ flex: 1 }}>
          Event type
        </Text>
        <Text size="xs" fw={600} c="dimmed" w={120}>
          Status
        </Text>
        <Text size="xs" fw={600} c="dimmed" w={90}>
          Duration
        </Text>
        <Text size="xs" fw={600} c="dimmed" w={140} ta="right">
          Actions
        </Text>
      </Group>
      {data.map((d) => (
        <Group
          key={d.id}
          px="sm"
          py="sm"
          wrap="nowrap"
          style={{
            borderBottom: "1px solid var(--mantine-color-default-border)",
          }}
        >
          <Text size="xs" c="dimmed" w={170} ff="monospace" truncate>
            {(d.dispatchedAt || d.createdAt || "")
              .replace("T", " ")
              .slice(0, 19) || "—"}
          </Text>
          <Box style={{ flex: 1, minWidth: 0 }}>
            <Code bg="indigo.1" c="indigo.9">
              {d.eventType}
            </Code>
          </Box>
          <Box w={120}>
            <DispatchBadge status={d.status} />
          </Box>
          <Text size="xs" c="dimmed" w={90}>
            {d.durationMs ? `${d.durationMs} ms` : "—"}
          </Text>
          <Group w={140} justify="flex-end" gap="xs" wrap="nowrap">
            <Button
              variant="subtle"
              color="gray"
              size="xs"
              leftSection={<RotateCcw size={12} />}
              onClick={() => setReplayTarget(d)}
              disabled={d.status === "pending" || d.status === "processing"}
            >
              Replay
            </Button>
          </Group>
        </Group>
      ))}
    </Stack>
  )
}

export default function EventRoutingDetail() {
  const navigate = useNavigate()
  const { id } = useParams()
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const flagEnabled = isFeatureFlagEnabled(FEATURE_FLAG)

  const subscriptions = useEventRoutingStore((s) => s.subscriptions)
  const dispatches = useEventRoutingStore((s) => s.dispatches)
  const fetchAll = useEventRoutingStore((s) => s.fetchAll)
  const fetchDispatches = useEventRoutingStore((s) => s.fetchDispatches)
  const togglePause = useEventRoutingStore((s) => s.togglePause)
  const deleteSubscription = useEventRoutingStore((s) => s.deleteSubscription)

  const sub = subscriptions.data.find((s) => s.id === id) || null

  useEffect(() => {
    if (flagEnabled && subscriptions.status === "idle") {
      fetchAll()
    }
  }, [flagEnabled, subscriptions.status, fetchAll])

  useEffect(() => {
    if (id) fetchDispatches(id)
  }, [id, fetchDispatches])

  const [deleteOpened, deleteControls] = useDisclosure(false)
  const [deleting, setDeleting] = useState(false)

  if (!flagEnabled) {
    return <Navigate to="/" replace />
  }
  if (subscriptions.status === "loading" || subscriptions.status === "idle") {
    return <PageLoader h={400} />
  }
  if (subscriptions.status === "error") {
    return <Text c="red">{subscriptions.error}</Text>
  }
  if (!sub) {
    return (
      <Stack gap="md">
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() => navigate("/features/event-routing")}
          px={0}
          w="fit-content"
        >
          Back
        </Button>
        <Text size="sm" c="dimmed">
          Subscription not found.
        </Text>
      </Stack>
    )
  }

  const failedDispatches = (dispatches[sub.id]?.data || []).filter(
    (d) => d.status === "failed" || d.status === "error",
  )
  const lastFailureReason = failedDispatches[0]?.lastError

  const handleTogglePause = async () => {
    try {
      await togglePause(sub.id)
      notifications.show({
        message:
          sub.status === "active"
            ? "Subscription paused."
            : "Subscription resumed.",
        color: "green",
      })
    } catch (e) {
      notifications.show({
        message: e?.response?.data?.message || "Failed.",
        color: "red",
      })
    }
  }

  const handleConfirmDelete = async () => {
    setDeleting(true)
    try {
      await deleteSubscription(sub.id)
      notifications.show({ message: `Deleted "${sub.name}".`, color: "green" })
      navigate("/features/event-routing")
    } catch (e) {
      notifications.show({
        message: e?.response?.data?.message || "Failed to delete.",
        color: "red",
      })
      setDeleting(false)
    }
  }

  return (
    <Stack gap={0}>
      <Box>
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() => navigate("/features/event-routing")}
          px={0}
          w="fit-content"
          mb="xl"
        >
          Back
        </Button>
      </Box>

      <Group
        justify="space-between"
        align="flex-start"
        mb="xl"
        gap="md"
        wrap="nowrap"
      >
        <Stack gap="xs" style={{ flex: 1, minWidth: 0 }}>
          <Group gap="sm" align="center">
            <Title order={1}>{sub.name}</Title>
            <StatusBadge status={sub.status} />
          </Group>
          {sub.description && (
            <Text size="sm" c="dimmed">
              {sub.description}
            </Text>
          )}
        </Stack>
        <Group gap="sm" wrap="nowrap">
          <Button
            size="sm"
            variant="light"
            color={sub.status === "active" ? "gray" : "green"}
            leftSection={
              sub.status === "active" ? <Pause size={14} /> : <Play size={14} />
            }
            onClick={handleTogglePause}
          >
            {sub.status === "active" ? "Pause" : "Resume"}
          </Button>
          <Button
            size="sm"
            variant="solid"
            leftSection={<Pencil size={14} />}
            onClick={() => navigate(`/features/event-routing/${sub.id}/edit`)}
          >
            Edit
          </Button>
          <Tooltip label="Delete subscription">
            <ActionIcon
              variant="light"
              color="red"
              size={36}
              onClick={deleteControls.open}
            >
              <Trash2 size={16} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>

      {sub.status === "paused" && (
        <Alert color="yellow" icon={<Pause size={16} />} mb="xl">
          This subscription is paused. Incoming events will not be dispatched.
        </Alert>
      )}

      <Stack gap="xxlAlt">
        <Stack gap="sm">
          <SectionHeader
            title="Target runbook"
            subtitle="Runbook session dispatched when the subscribed event fires."
          />
          <TargetRunbookCard sub={sub} />
        </Stack>

        <Stack gap="sm">
          <SectionHeader
            title="Subscribed event"
            subtitle="The platform event that triggers this subscription."
          />
          <Group gap="xs">
            {sub.eventTypes?.[0] ? (
              <EventChip name={sub.eventTypes[0]} />
            ) : (
              <Text size="xs" c="dimmed">
                No event configured.
              </Text>
            )}
          </Group>
        </Stack>

        <Stack gap="sm">
          <SectionHeader
            title="Parameter mapping"
            subtitle="Maps event payload fields to the runbook's input parameters."
          />
          <MappingTable mapping={sub.parameterMapping} />
        </Stack>

        <Stack gap="sm">
          <SectionHeader title="Dispatch history" />
          <Card padding={0} withBorder>
            <DispatchHistory subId={sub.id} />
          </Card>
        </Stack>

        {failedDispatches.length > 0 && (
          <Alert color="red" icon={<TriangleAlert size={16} />}>
            {`${failedDispatches.length} failed dispatch${failedDispatches.length === 1 ? "" : "es"} in the recent history.`}
            {lastFailureReason ? ` Last reason: ${lastFailureReason}.` : ""}
          </Alert>
        )}
      </Stack>

      <ReplayDispatchModal subId={sub.id} />
      <Modal
        opened={deleteOpened}
        onClose={deleteControls.close}
        title="Delete subscription?"
        size="sm"
      >
        <Stack>
          <Text size="sm">
            {`This will stop routing matching events to `}
            <Text component="span" fw={600}>
              {sub.name}
            </Text>
            {`. Existing session history is kept for audit, but the subscription cannot be restored.`}
          </Text>
          <Group justify="flex-end" mt="xs">
            <Button
              variant="subtle"
              color="gray"
              onClick={deleteControls.close}
            >
              Cancel
            </Button>
            <Button
              color="red"
              loading={deleting}
              onClick={handleConfirmDelete}
            >
              Delete subscription
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}
