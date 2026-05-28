import { useEffect, useMemo, useRef, useState } from "react"
import { Navigate, useNavigate, useParams } from "react-router-dom"
import { Anchor, Box, Card, Grid, Group, Stack, Text, Title } from "@mantine/core"
import { useInViewport } from "@mantine/hooks"
import { notifications } from "@mantine/notifications"
import { ArrowLeft, ArrowRight } from "lucide-react"
import Button from "@/components/Button"
import Code from "@/components/Code"
import PageLoader from "@/components/PageLoader"
import Radio from "@/components/Radio"
import Select from "@/components/Select"
import TextInput from "@/components/TextInput"
import Textarea from "@/components/Textarea"
import { useUserStore } from "@/stores/useUserStore"
import { useEventRoutingStore } from "../store"
import EventDescription from "../components/EventDescription"

const FEATURE_FLAG = "experimental.event_routing"

function extractRepoName(repository) {
  if (!repository) return ""
  const tail = repository.split("/").filter(Boolean).pop() || repository
  return tail.replace(/\.git$/, "")
}

function sortRunbookParams(metadata) {
  return Object.entries(metadata || {})
    .map(([name, info]) => [name, info || {}])
    .sort(([aName, a], [bName, b]) => {
      const ao =
        typeof a.order === "number" ? a.order : Number.POSITIVE_INFINITY
      const bo =
        typeof b.order === "number" ? b.order : Number.POSITIVE_INFINITY
      if (ao !== bo) return ao - bo
      return aName.localeCompare(bName)
    })
}

function SectionRow({ title, description, children }) {
  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Title order={4}>{title}</Title>
          <Text size="sm" c="dimmed">
            {description}
          </Text>
        </Stack>
      </Grid.Col>
      <Grid.Col span={5}>{children}</Grid.Col>
    </Grid>
  )
}

export default function EventRoutingForm() {
  const navigate = useNavigate()
  const { id } = useParams()
  const isEdit = Boolean(id)

  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()

  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const flagEnabled = isFeatureFlagEnabled(FEATURE_FLAG)

  const subscriptions = useEventRoutingStore((s) => s.subscriptions)
  const catalog = useEventRoutingStore((s) => s.catalog.data)
  const connections = useEventRoutingStore((s) => s.connections.data)
  const runbooksByConnection = useEventRoutingStore(
    (s) => s.runbooksByConnection,
  )
  const fetchAll = useEventRoutingStore((s) => s.fetchAll)
  const fetchRunbooksForConnection = useEventRoutingStore(
    (s) => s.fetchRunbooksForConnection,
  )
  const createSubscription = useEventRoutingStore((s) => s.createSubscription)
  const updateSubscription = useEventRoutingStore((s) => s.updateSubscription)
  const submitting = useEventRoutingStore((s) => s.submitting)

  useEffect(() => {
    if (flagEnabled) fetchAll()
  }, [flagEnabled, fetchAll])

  const sub = isEdit
    ? subscriptions.data.find((s) => s.id === id) || null
    : null

  const [form, setForm] = useState({
    name: "",
    description: "",
    connectionName: "",
    runbookValue: "",
    selectedEvent: "",
  })
  const [mapping, setMapping] = useState({})

  const editPrefillDone = useRef(false)
  const runbookPrefillDone = useRef(false)

  useEffect(() => {
    if (!isEdit || !sub || editPrefillDone.current) return
    const existingMapping = Object.fromEntries(
      Object.entries(sub.parameterMapping || {}).map(([k, v]) => [
        k,
        v.startsWith("$.") ? v.slice(2) : v,
      ]),
    )
    setForm((f) => ({
      ...f,
      name: sub.name || "",
      description: sub.description || "",
      connectionName: sub.connectionName || "",
      selectedEvent: (sub.eventTypes || [])[0] || "",
    }))
    setMapping(existingMapping)
    editPrefillDone.current = true
  }, [isEdit, sub])

  useEffect(() => {
    if (form.connectionName) fetchRunbooksForConnection(form.connectionName)
  }, [form.connectionName, fetchRunbooksForConnection])

  const { runbookOptions, runbookLookup } = useMemo(() => {
    const opts = []
    const lookup = {}
    const repos = runbooksByConnection[form.connectionName]?.data || []
    repos.forEach((repo, ri) => {
      const repoName = extractRepoName(repo.repository)
      ;(repo.items || []).forEach((item, ii) => {
        const value = `${ri}|${ii}`
        opts.push({ value, label: `@${repoName}/${item.name}` })
        lookup[value] = {
          repository: repo.repository,
          file: item.name,
          metadata: item.metadata || {},
        }
      })
    })
    return { runbookOptions: opts, runbookLookup: lookup }
  }, [form.connectionName, runbooksByConnection])

  useEffect(() => {
    if (!isEdit || !sub || runbookPrefillDone.current) return
    const runbooksStatus = runbooksByConnection[form.connectionName]?.status
    if (runbooksStatus !== "ready") return
    const entries = Object.entries(runbookLookup)
    const match = entries.find(
      ([, v]) =>
        v.repository === sub.runbookRepository && v.file === sub.runbookFile,
    )
    if (match) {
      setForm((f) => ({ ...f, runbookValue: match[0] }))
    }
    runbookPrefillDone.current = true
  }, [isEdit, sub, runbookLookup, runbooksByConnection, form.connectionName])

  const selectedRunbook = runbookLookup[form.runbookValue] || null
  const runbookParams = useMemo(
    () => sortRunbookParams(selectedRunbook?.metadata),
    [selectedRunbook],
  )

  const selectedEventEntry = useMemo(
    () => catalog.find((e) => e.name === form.selectedEvent) || null,
    [catalog, form.selectedEvent],
  )

  const eventFieldOptions = useMemo(() => {
    const schema = selectedEventEntry?.schema || []
    return schema.map((f) => ({
      value: f.name,
      label: f.type ? `${f.name}  ·  ${f.type}` : f.name,
    }))
  }, [selectedEventEntry])

  useEffect(() => {
    if (isEdit && !editPrefillDone.current) return
    if (!selectedRunbook || !selectedEventEntry) {
      if (!isEdit) setMapping({})
      return
    }
    if (isEdit && runbookPrefillDone.current) return
    const eventFieldSet = new Set(
      (selectedEventEntry.schema || []).map((f) => f.name),
    )
    const seed = {}
    for (const [paramName] of runbookParams) {
      if (eventFieldSet.has(paramName)) seed[paramName] = paramName
    }
    setMapping(seed)
  }, [
    form.runbookValue,
    form.selectedEvent,
    selectedRunbook,
    selectedEventEntry,
    runbookParams,
    isEdit,
  ])

  const runbooksStatus =
    runbooksByConnection[form.connectionName]?.status || "idle"
  const noRunbooks =
    form.connectionName &&
    runbooksStatus === "ready" &&
    runbookOptions.length === 0

  const grouped = useMemo(() => {
    const m = {}
    for (const e of catalog) {
      if (!m[e.category]) m[e.category] = []
      m[e.category].push(e)
    }
    return Object.entries(m).sort(([a], [b]) => a.localeCompare(b))
  }, [catalog])

  const missingRequiredMapping = runbookParams.some(
    ([name, info]) => info.required && !mapping[name],
  )

  const canSubmit =
    form.name.trim().length > 0 &&
    form.connectionName.trim().length > 0 &&
    form.runbookValue.length > 0 &&
    form.selectedEvent.length > 0 &&
    !missingRequiredMapping

  const selectEvent = (name) => setForm((f) => ({ ...f, selectedEvent: name }))

  const setMappingFor = (paramName, eventFieldName) => {
    setMapping((prev) => {
      const next = { ...prev }
      if (eventFieldName) next[paramName] = eventFieldName
      else delete next[paramName]
      return next
    })
  }

  const handleSave = async () => {
    if (!canSubmit) return
    if (!selectedRunbook) {
      notifications.show({
        message: "Pick a runbook before saving.",
        color: "red",
      })
      return
    }
    const parameterMapping = Object.fromEntries(
      Object.entries(mapping).map(([param, field]) => [param, `$.${field}`]),
    )
    const payload = {
      name: form.name.trim(),
      description: form.description.trim(),
      runbookRepository: selectedRunbook.repository,
      runbookFile: selectedRunbook.file,
      connectionName: form.connectionName.trim(),
      eventTypes: [form.selectedEvent],
      parameterMapping,
    }
    try {
      if (isEdit) {
        await updateSubscription(id, payload)
        notifications.show({
          message: "Subscription updated.",
          color: "green",
        })
        navigate(`/features/event-routing/${id}`)
      } else {
        await createSubscription(payload)
        notifications.show({
          message: "Subscription created.",
          color: "green",
        })
        navigate("/features/event-routing")
      }
    } catch (e) {
      notifications.show({
        message:
          e?.response?.data?.message ||
          (isEdit
            ? "Failed to update subscription."
            : "Failed to create subscription."),
        color: "red",
      })
    }
  }

  const connectionOptions = (connections || []).map((c) => ({
    value: c.name || c,
    label: c.name || c,
  }))

  if (!flagEnabled) return <Navigate to="/" replace />

  if (
    isEdit &&
    (subscriptions.status === "loading" || subscriptions.status === "idle")
  ) {
    return <PageLoader h={400} />
  }

  return (
    <Stack gap={0}>
      <Box>
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() =>
            navigate(
              isEdit
                ? `/features/event-routing/${id}`
                : "/features/event-routing",
            )
          }
          px={0}
          w="fit-content"
          mb="xl"
        >
          Back
        </Button>
      </Box>

      <div ref={sentinelRef} aria-hidden="true" />
      <Group
        justify="space-between"
        align="center"
        pos="sticky"
        top={0}
        bg="var(--mantine-color-body)"
        py="md"
        mb="xxlAlt"
        style={{
          zIndex: 10,
          borderBottom: headerInView
            ? "1px solid transparent"
            : "1px solid var(--mantine-color-default-border)",
        }}
      >
        <Title order={1}>
          {isEdit ? "Edit subscription" : "Create subscription"}
        </Title>
        <Group gap="sm">
          <Button
            variant="subtle"
            color="gray"
            onClick={() =>
              navigate(
                isEdit
                  ? `/features/event-routing/${id}`
                  : "/features/event-routing",
              )
            }
          >
            Cancel
          </Button>
          <Button
            onClick={handleSave}
            disabled={!canSubmit}
            loading={submitting}
          >
            {"Save"}
          </Button>
        </Group>
      </Group>

      <Stack gap="xxlAlt">
        <SectionRow
          title="Subscription information"
          description="Identify this subscription so reviewers know what it is and when it fires."
        >
          <Stack gap="md">
            <TextInput
              label="Name"
              placeholder="e.g. Auto-revoke AI access on PII"
              value={form.name}
              onChange={(e) => {
                const value = e.currentTarget.value
                setForm((f) => ({ ...f, name: value }))
              }}
              required
              autoFocus
            />
            <Textarea
              label="Description (Optional)"
              placeholder="What this subscription is for and when it should fire"
              value={form.description}
              onChange={(e) => {
                const value = e.currentTarget.value
                setForm((f) => ({ ...f, description: value }))
              }}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="Target runbook"
          description="Pick the resource role first, then the runbook to execute when matching events fire."
        >
          <Stack gap="md">
            <Select
              label="Resource role"
              withAsterisk
              placeholder="Select a resource role"
              data={connectionOptions}
              value={form.connectionName || null}
              onChange={(v) =>
                setForm((f) => ({
                  ...f,
                  connectionName: v || "",
                  runbookValue: "",
                }))
              }
              searchable
              nothingFoundMessage="No resource roles"
            />
            <Select
              label="Runbook"
              withAsterisk
              placeholder={
                !form.connectionName
                  ? "Select a resource role first"
                  : runbooksStatus === "loading"
                    ? "Loading runbooks…"
                    : noRunbooks
                      ? "No runbooks for this resource role"
                      : "Select a runbook"
              }
              data={runbookOptions}
              value={form.runbookValue || null}
              onChange={(v) =>
                setForm((f) => ({ ...f, runbookValue: v || "" }))
              }
              searchable
              disabled={
                !form.connectionName || runbooksStatus !== "ready" || noRunbooks
              }
              nothingFoundMessage="No runbooks match"
            />
            {noRunbooks && (
              <Text size="xs" c="dimmed">
                {"No runbooks are configured for this resource role. "}
                <Anchor href="/features/runbooks/setup" size="xs">
                  Set up runbooks
                </Anchor>
                {" to make them available here."}
              </Text>
            )}
            {runbooksStatus === "error" && (
              <Text size="xs" c="red">
                {runbooksByConnection[form.connectionName]?.error ||
                  "Failed to load runbooks for this resource role."}
              </Text>
            )}
          </Stack>
        </SectionRow>

        <SectionRow
          title="Subscribed event"
          description="Pick the platform event that should trigger this runbook."
        >
          <Radio.Group
            value={form.selectedEvent}
            onChange={selectEvent}
            withAsterisk
          >
            <Box
              mah={480}
              style={{
                overflow: "auto",
                border: "1px solid var(--mantine-color-default-border)",
                borderRadius: "var(--mantine-radius-sm)",
              }}
            >
              {grouped.length === 0 ? (
                <Text size="xs" c="dimmed" p="md">
                  No events available in the catalog.
                </Text>
              ) : (
                grouped.map(([cat, events]) => (
                  <Box key={cat}>
                    <Box
                      px="sm"
                      py="xs"
                      bg="gray.0"
                      style={{
                        borderBottom:
                          "1px solid var(--mantine-color-default-border)",
                      }}
                    >
                      <Text size="xs" fw={600} c="dimmed" tt="uppercase">
                        {cat}
                      </Text>
                    </Box>
                    {events.map((e) => (
                      <Group
                        key={e.name}
                        px="sm"
                        py="xs"
                        align="flex-start"
                        gap="sm"
                        wrap="nowrap"
                        bg="white"
                        style={{
                          borderBottom:
                            "1px solid var(--mantine-color-default-border)",
                          cursor: "pointer",
                        }}
                        onClick={() => selectEvent(e.name)}
                      >
                        <Radio value={e.name} mt={4} />
                        <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
                          <Code bg="indigo.1" c="indigo.9">{e.name}</Code>
                          <EventDescription text={e.description} size="xs" c="dimmed" />
                        </Stack>
                      </Group>
                    ))}
                  </Box>
                ))
              )}
            </Box>
          </Radio.Group>
        </SectionRow>

        <SectionRow
          title="Parameter mapping"
          description="For each parameter the runbook declares, pick the field from the event payload to pass in. Fields with the same name are pre-matched."
        >
          {!selectedRunbook || !selectedEventEntry ? (
            <Text size="xs" c="dimmed">
              Pick a runbook and event above to configure the mapping.
            </Text>
          ) : runbookParams.length === 0 ? (
            <Text size="xs" c="dimmed">
              This runbook declares no parameters. The dispatch will run without
              an input mapping.
            </Text>
          ) : eventFieldOptions.length === 0 ? (
            <Text size="xs" c="red">
              The selected event has no schema fields available to map.
            </Text>
          ) : (
            <Card padding={0} withBorder>
              <Stack gap={0}>
                <Group
                  px="md"
                  py="sm"
                  wrap="nowrap"
                  gap="md"
                  bg="gray.0"
                  style={{
                    borderBottom:
                      "1px solid var(--mantine-color-default-border)",
                  }}
                >
                  <Text
                    size="xs"
                    fw={600}
                    c="dimmed"
                    tt="uppercase"
                    style={{ flex: 1, minWidth: 0 }}
                  >
                    Runbook parameter
                  </Text>
                  <Box w={16} />
                  <Text
                    size="xs"
                    fw={600}
                    c="dimmed"
                    tt="uppercase"
                    style={{ flex: 1.2, minWidth: 0 }}
                  >
                    Event payload field
                  </Text>
                </Group>
                {runbookParams.map(([paramName, paramInfo]) => {
                  const required = !!paramInfo.required
                  const value = mapping[paramName] || null
                  const description = paramInfo.description || ""
                  return (
                    <Group
                      key={paramName}
                      p="md"
                      align="flex-start"
                      wrap="nowrap"
                      gap="md"
                      style={{
                        borderBottom:
                          "1px solid var(--mantine-color-default-border)",
                      }}
                    >
                      <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
                        <Group gap="xs" align="center">
                          <Code>{paramName}</Code>
                          {required && (
                            <Text size="xs" c="red">
                              *
                            </Text>
                          )}
                          {paramInfo.type && (
                            <Text size="xs" c="dimmed">
                              {paramInfo.type}
                            </Text>
                          )}
                        </Group>
                        {description && (
                          <Text size="xs" c="dimmed">
                            {description}
                          </Text>
                        )}
                      </Stack>
                      <ArrowRight
                        size={16}
                        color="var(--mantine-color-gray-6)"
                        style={{ marginTop: 6 }}
                      />
                      <Box style={{ flex: 1.2, minWidth: 0 }}>
                        <Select
                          placeholder={
                            required ? "Pick an event field" : "Optional"
                          }
                          data={eventFieldOptions}
                          value={value}
                          onChange={(v) => setMappingFor(paramName, v)}
                          searchable
                          clearable
                          error={required && !value ? "Required" : undefined}
                        />
                      </Box>
                    </Group>
                  )
                })}
              </Stack>
            </Card>
          )}
        </SectionRow>
      </Stack>
    </Stack>
  )
}
