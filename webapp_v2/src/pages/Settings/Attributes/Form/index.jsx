import { useState, useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Button, Grid, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { ArrowLeft } from 'lucide-react'
import { useMinDelay } from '@/hooks/useMinDelay'
import PageLoader from '@/components/PageLoader'
import Modal from '@/components/Modal'
import TextInput from '@/components/TextInput'
import MultiSelect from '@/components/MultiSelect'
import { attributesService } from '@/services/attributes'
import { connectionsService } from '@/services/connections'

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

export default function AttributesForm() {
  const navigate = useNavigate()
  const { name: attrName } = useParams()
  const isEdit = Boolean(attrName)

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [connectionNames, setConnectionNames] = useState([])
  const [availableConnections, setAvailableConnections] = useState([])
  const [loading, setLoading] = useState(isEdit)
  const [saving, setSaving] = useState(false)

  const [deleteOpened, { open: openDelete, close: closeDelete }] = useDisclosure(false)

  const showLoader = useMinDelay(loading)

  useEffect(() => {
    async function loadData() {
      try {
        const connectionsRes = await connectionsService.getConnections()
        const connList = Array.isArray(connectionsRes) ? connectionsRes : (connectionsRes ?? [])
        setAvailableConnections(connList.map((c) => ({ value: c.name, label: c.name })))

        if (isEdit) {
          const attrRes = await attributesService.get(attrName)
          const attr = attrRes.data
          setName(attr.name ?? '')
          setDescription(attr.description ?? '')
          setConnectionNames(attr.connection_names ?? [])
        }
      } catch {
        notifications.show({ message: 'Failed to load data.', color: 'red' })
      } finally {
        setLoading(false)
      }
    }
    loadData()
  }, [attrName, isEdit])

  async function handleSubmit() {
    if (!name.trim()) {
      notifications.show({ message: 'Name is required.', color: 'red' })
      return
    }
    setSaving(true)
    try {
      const body = { name, connection_names: connectionNames }
      if (description.trim()) body.description = description
      if (isEdit) {
        await attributesService.update(attrName, body)
        notifications.show({ message: 'Attribute updated.', color: 'green' })
      } else {
        await attributesService.create(body)
        notifications.show({ message: 'Attribute created.', color: 'green' })
      }
      navigate('/settings/attributes')
    } catch {
      notifications.show({ message: `Failed to ${isEdit ? 'update' : 'create'} attribute.`, color: 'red' })
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    setSaving(true)
    try {
      await attributesService.remove(attrName)
      notifications.show({ message: 'Attribute deleted.', color: 'green' })
      navigate('/settings/attributes')
    } catch {
      notifications.show({ message: 'Failed to delete attribute.', color: 'red' })
    } finally {
      setSaving(false)
      closeDelete()
    }
  }

  if (showLoader) return <PageLoader />

  return (
    <Stack gap={0}>
      <Box>
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() => navigate('/settings/attributes')}
          px={0}
          w="fit-content"
          mb="xl"
        >
          Back
        </Button>
      </Box>

      <Group justify="space-between" align="center" mb="xxxl">
        <Title order={1}>{isEdit ? 'Configure Attribute' : 'Create Attribute'}</Title>
        <Group gap="sm">
          {isEdit && (
            <Button variant="subtle" color="red" onClick={openDelete} disabled={saving}>
              Delete
            </Button>
          )}
          <Button onClick={handleSubmit} loading={saving}>
            Save
          </Button>
        </Group>
      </Group>

      <Stack gap="xxl">
        <SectionRow
          title="Set Attribute information"
          description="Used to identify Attributes on resources."
        >
          <Stack gap="md">
            <TextInput
              label="Name"
              placeholder="e.g. engineering"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
              disabled={isEdit}
              required
              autoFocus={!isEdit}
            />
            <TextInput
              label="Description (Optional)"
              placeholder="Describe how this attribute is used"
              value={description}
              onChange={(e) => setDescription(e.currentTarget.value)}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="Role configuration"
          description="Select which Roles to apply this configuration."
        >
          <MultiSelect
            label="Connections"
            placeholder="Select connections…"
            data={availableConnections}
            value={connectionNames}
            onChange={setConnectionNames}
            searchable
            clearable
          />
        </SectionRow>
      </Stack>

      <Modal opened={deleteOpened} onClose={closeDelete} title="Delete Attribute">
        <Stack gap="md">
          <Text size="sm">
            {`Are you sure you want to delete the attribute '${attrName}'? This action cannot be undone.`}
          </Text>
          <Group justify="flex-end" gap="sm">
            <Button variant="subtle" color="gray" onClick={closeDelete}>
              Cancel
            </Button>
            <Button color="red" onClick={handleDelete} loading={saving}>
              Delete
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}
