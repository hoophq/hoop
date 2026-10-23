import {
  Title,
  Button,
  Group,
  Text,
  Badge,
  Stack,
  Modal,
  Card,
  Box,
  Flex,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Trash2, CircleDashed, Tags } from 'lucide-react'
import { useAgentStore } from '@/stores/useAgentStore'
import { useUserStore } from '@/stores/useUserStore'
import EmptyState from '@/layout/EmptyState'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import { docsUrl } from '@/utils/docsUrl'
import PageLoader from '@/components/PageLoader'
import TagsEditor from '@/components/TagsEditor'
import TagBadge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { showSnackbar } from '@/utils/snackbar'
import { labelForTag, AGENT_TAG_RULES } from '@/utils/tags'

function AgentStatusBadge({ status }) {
  const isOnline = status === 'CONNECTED'
  return (
    <Badge
      color={isOnline ? 'green' : 'red'}
      variant="light"
      leftSection={<CircleDashed size={10} />}
    >
      {isOnline ? 'Online' : 'Offline'}
    </Badge>
  )
}

function DeleteAgentModal({ agent, opened, onClose, onConfirm }) {
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Delete agent?"
      centered
      size="sm"
    >
      <Stack>
        <Stack gap={4}>
          <Text size="sm">
            This action will instantly remove the agent{' '}
            <Text component="span" fw={600}>{agent?.name}</Text>{' '}
            and cannot be undone.
          </Text>
          <Text size="sm">Are you sure you want to delete this agent?</Text>
        </Stack>
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose}>
            Cancel
          </Button>
          <Button color="red" onClick={onConfirm}>
            Confirm and delete
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

function EditTagsModal({ agent, opened, onClose }) {
  const updateAgentTags = useAgentStore((s) => s.updateAgentTags)
  const tagsPool = useAgentStore((s) => s.tagsPool)
  // The parent remounts this modal on every open (key), so the draft
  // always starts from the agent's saved tags.
  const [tags, setTags] = useState(() => ({ ...(agent?.tags || {}) }))
  const [saving, setSaving] = useState(false)

  const handleSave = async () => {
    setSaving(true)
    try {
      await updateAgentTags(agent.id, tags)
      showSnackbar({ level: 'success', text: `Tags of agent "${agent.name}" updated.` })
      onClose()
    } catch (error) {
      showSnackbar({
        level: 'error',
        text: 'Failed to update agent tags.',
        description: error?.response?.data?.message,
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal opened={opened} onClose={onClose} title={`Edit tags of ${agent?.name ?? ''}`} centered size="lg">
      <Stack>
        <Text size="sm" c="dimmed">
          Labels to select agents through the API, for example production agents only.
        </Text>
        <TagsEditor value={tags} onChange={setTags} pool={tagsPool} rules={AGENT_TAG_RULES} />
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSave} loading={saving}>
            Save
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

function AgentTags({ tags }) {
  const entries = Object.entries(tags || {})
  if (entries.length === 0) return null
  return (
    <Group gap="xs" mt="xs">
      {entries.map(([key, value]) => (
        <Tooltip key={key} label={key}>
          <TagBadge tag chip variant="light" color="gray">
            {`${labelForTag(key)}: ${value}`}
          </TagBadge>
        </Tooltip>
      ))}
    </Group>
  )
}

function AgentRow({ agent, isAdmin, isLast, onDelete, onEditTags }) {
  return (
    <Box
      p="lg"
      style={isLast ? undefined : { borderBottom: '1px solid var(--mantine-color-default-border)' }}
    >
      <Flex align="center">
        <Box style={{ flex: 1 }}>
          <Text size="lg" fw={700}>{agent.name}</Text>
          <Text size="xs" c="dimmed">Version: {agent.version}</Text>
          <Text size="xs" c="dimmed">ID: {agent.id}</Text>
          <AgentTags tags={agent.tags} />
        </Box>
        <Flex align="center" gap="md">
          <AgentStatusBadge status={agent.status} />
          {isAdmin && (
            <Button
              size="sm"
              variant="light"
              color="gray"
              leftSection={<Tags size={14} />}
              onClick={() => onEditTags(agent)}
            >
              Edit tags
            </Button>
          )}
          {isAdmin && (
            <Button
              size="sm"
              variant="light"
              color="red"
              leftSection={<Trash2 size={14} />}
              onClick={() => onDelete(agent)}
            >
              Delete
            </Button>
          )}
        </Flex>
      </Flex>
    </Box>
  )
}

function Agents() {
  const navigate = useNavigate()
  const { agents, loading, error, fetchAgents, deleteAgent, fetchTagsPool } = useAgentStore()
  const { user } = useUserStore()
  const [opened, { open, close }] = useDisclosure(false)
  const [tagsOpened, { open: openTags, close: closeTags }] = useDisclosure(false)
  const [selectedAgent, setSelectedAgent] = useState(null)
  const [tagsAgent, setTagsAgent] = useState(null)
  const [tagsSession, setTagsSession] = useState(0)

  const isAdmin = user?.is_admin
  const showLoader = loading

  useEffect(() => {
    fetchAgents()
    fetchTagsPool()
  }, [fetchAgents, fetchTagsPool])

  const handleEditTagsClick = (agent) => {
    setTagsAgent(agent)
    setTagsSession((n) => n + 1)
    openTags()
  }

  const handleDeleteClick = (agent) => {
    setSelectedAgent(agent)
    open()
  }

  const handleDeleteConfirm = async () => {
    try {
      await deleteAgent(selectedAgent.id)
      showSnackbar({ level: 'success', text: `Agent "${selectedAgent.name}" removed.` })
    } catch {
      showSnackbar({ level: 'error', text: 'Failed to delete agent.' })
    } finally {
      close()
      setSelectedAgent(null)
    }
  }

  if (showLoader) {
    return <PageLoader h={400} />
  }

  if (error) {
    return <Text c="red">{error}</Text>
  }

  return (
    <>
      <DeleteAgentModal
        agent={selectedAgent}
        opened={opened}
        onClose={close}
        onConfirm={handleDeleteConfirm}
      />
      <EditTagsModal key={tagsSession} agent={tagsAgent} opened={tagsOpened} onClose={closeTags} />

      <Stack gap="xxl">
        <Group justify="space-between" align="flex-start">
          <Stack gap="sm">
            <Title order={1}>Agents</Title>
            <Text size="md" c="dimmed">
              View and manage your Agents for your resource roles
            </Text>
            <DocsBtnCallOut text="Learn more about Agents" href={docsUrl.setup.agents} />
          </Stack>
          {isAdmin && agents.length > 0 && (
            <Button onClick={() => navigate('/agents/new')}>
              Setup new Agent
            </Button>
          )}
        </Group>

        {agents.length === 0 ? (
          <EmptyState
            title="No agents yet"
            description="Create a new Agent to your Organization to show it here"
            action={
              isAdmin
                ? { label: 'Setup new Agent', onClick: () => navigate('/agents/new') }
                : null
            }
          />
        ) : (
          <Card padding={0} withBorder>
            {agents.map((agent, i) => (
              <AgentRow
                key={agent.id}
                agent={agent}
                isAdmin={isAdmin}
                isLast={i === agents.length - 1}
                onDelete={handleDeleteClick}
                onEditTags={handleEditTagsClick}
              />
            ))}
          </Card>
        )}
      </Stack>
    </>
  )
}

export default Agents
