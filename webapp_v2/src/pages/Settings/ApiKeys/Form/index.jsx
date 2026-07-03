import { useState, useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Button, Grid, Group, Stack, Text, Title, Box } from '@mantine/core'
import { showSnackbar } from '@/utils/snackbar'
import { ArrowLeft } from 'lucide-react'
import { useMinDelay } from '@/hooks/useMinDelay'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import MultiSelect from '@/components/MultiSelect'
import { apiKeysService } from '@/services/apiKeys'
import { usersService } from '@/services/users'

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

export default function ApiKeysForm() {
  const navigate = useNavigate()
  const { id } = useParams()
  const isEdit = Boolean(id)

  const [name, setName] = useState('')
  const [groups, setGroups] = useState([])
  const [availableGroups, setAvailableGroups] = useState([])
  const [loading, setLoading] = useState(isEdit)
  const [saving, setSaving] = useState(false)

  const showLoader = useMinDelay(loading)

  useEffect(() => {
    async function loadData() {
      try {
        const [groupsRes, keyRes] = await Promise.all([
          usersService.listGroups(),
          isEdit ? apiKeysService.get(id) : Promise.resolve(null),
        ])
        const groupData = groupsRes.data ?? []
        setAvailableGroups(groupData.map((g) => ({ value: g, label: g })))
        if (keyRes) {
          const key = keyRes.data
          setName(key.name ?? '')
          setGroups(key.groups ?? [])
        }
      } catch {
        showSnackbar({ level: 'error', text: 'Failed to load data.' })
      } finally {
        setLoading(false)
      }
    }
    loadData()
  }, [id, isEdit])

  async function handleSubmit() {
    if (!name.trim()) {
      showSnackbar({ level: 'error', text: 'Name is required.' })
      return
    }
    setSaving(true)
    try {
      if (isEdit) {
        await apiKeysService.update(id, { name, groups })
        showSnackbar({ level: 'success', text: 'API key updated.' })
        navigate('/settings/api-keys')
      } else {
        const res = await apiKeysService.create({ name, groups })
        navigate('/settings/api-keys/created', { state: { key: res.data } })
      }
    } catch {
      showSnackbar({ level: 'error', text: `Failed to ${isEdit ? 'update' : 'create'} API key.` })
    } finally {
      setSaving(false)
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
          onClick={() => navigate('/settings/api-keys')}
          px={0}
          w="fit-content"
          mb="xl"
        >
          Back
        </Button>
      </Box>

      <Group justify="space-between" align="center" mb="xxxlAlt">
        <Title order={1}>{isEdit ? 'Configure API Key' : 'Create new API Key'}</Title>
        <Button onClick={handleSubmit} loading={saving}>
          Save
        </Button>
      </Group>

      <Stack gap="xxlAlt">
        <SectionRow
          title="Basic info"
          description="Give this API key a name so you can identify it later."
        >
          <TextInput
            label="Name"
            placeholder="e.g. ci-pipeline-key"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            required
          />
        </SectionRow>

        <SectionRow
          title="Groups"
          description="Assign user groups to restrict what this API key can access."
        >
          <MultiSelect
            label="Groups"
            placeholder="Select groups…"
            data={availableGroups}
            value={groups}
            onChange={setGroups}
            searchable
            clearable
          />
        </SectionRow>
      </Stack>
    </Stack>
  )
}
