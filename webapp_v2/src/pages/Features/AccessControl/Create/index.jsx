import { useEffect, useRef, useState } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import { Box, Grid, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import ConnectionsMultiSelect from '@/components/ConnectionsMultiSelect'
import Modal from '@/components/Modal'
import MultiSelect from '@/components/MultiSelect'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { showSnackbar } from '@/utils/snackbar'
import { useAccessControlStore } from '../store'
import {
  groupAttributeNames,
  groupsWithPermissions,
  isEditableAttribute,
} from '../helpers'
import classes from './Create.module.css'

const LIST_PATH = '/features/access-control'

function SectionRow({ title, description, children }) {
  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Title order={4} fw={500}>
            {title}
          </Title>
          <Text size="sm" c="dimmed">
            {description}
          </Text>
        </Stack>
      </Grid.Col>
      <Grid.Col span={5}>{children}</Grid.Col>
    </Grid>
  )
}

// Remounted via `key` when the edited group changes, so state derives from the
// loaded plugin and attributes with lazy useState initializers instead of a
// prefill effect.
function GroupFormFields({ groupName, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)

  const plugin = useAccessControlStore((s) => s.plugin)
  const attributes = useAccessControlStore((s) => s.attributes)
  const submitting = useAccessControlStore((s) => s.submitting)
  const saveGroup = useAccessControlStore((s) => s.saveGroup)
  const deleteGroup = useAccessControlStore((s) => s.deleteGroup)

  const [name, setName] = useState(groupName ?? '')
  const [connectionIds, setConnectionIds] = useState(
    () => (groupsWithPermissions(plugin?.connections)[groupName] ?? []).map((c) => c.id),
  )
  const [attributeNames, setAttributeNames] = useState(() =>
    groupAttributeNames(attributes, groupName),
  )

  // A save is three writes (group, plugin, attributes) with no transaction
  // behind them. When a later one fails the group already exists, so a retry
  // must not POST it again — that would 409 and dead-end the admin.
  const groupCreatedRef = useRef(false)

  const canSubmit = name.trim().length > 0 && !submitting

  const handleSubmit = async (event) => {
    event.preventDefault()
    if (!canSubmit) return

    const targetName = isEdit ? groupName : name.trim()
    const result = await saveGroup({
      name: targetName,
      connectionIds,
      attributeNames,
      isNew: !isEdit && !groupCreatedRef.current,
    })
    if (result.groupCreated) groupCreatedRef.current = true

    if (result.ok) {
      showSnackbar({
        level: 'success',
        text: isEdit
          ? 'Group permissions updated.'
          : `Group '${targetName}' created.`,
      })
      navigate(LIST_PATH)
      return
    }
    showSnackbar({
      level: 'error',
      text:
        result.error?.response?.data?.message ||
        (isEdit ? 'Failed to update group.' : 'Failed to create group.'),
    })
  }

  const handleDelete = async () => {
    const { ok, error } = await deleteGroup(groupName)
    deleteModal.close()
    if (ok) {
      showSnackbar({ level: 'success', text: `Group '${groupName}' deleted.` })
      navigate(LIST_PATH)
      return
    }
    showSnackbar({
      level: 'error',
      text: error?.response?.data?.message || 'Failed to delete group.',
    })
  }

  // Protection profiles own their attributes and the API rejects updates to
  // them, so they are not offered here.
  const attributeOptions = attributes
    .filter(isEditableAttribute)
    .map((a) => ({ value: a.name, label: a.name }))

  return (
    <form onSubmit={handleSubmit}>
      <Stack gap={0}>
        <Box>
          <Button
            variant="transparent"
            color="gray"
            leftSection={<ArrowLeft size={16} />}
            onClick={() => navigate(LIST_PATH)}
            px={0}
            w="fit-content"
            mb="xl"
            type="button"
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
          mb="xl"
          mx={-PAGE_PADDING}
          px={PAGE_PADDING}
          className={classes.stickyHeader}
          data-scrolled={!headerInView || undefined}
        >
          <Title order={2} lts="-0.00625em">
            {isEdit ? `Edit group: ${groupName}` : 'Create new access control group'}
          </Title>
          <Group gap="sm">
            {isEdit && (
              <Button
                variant="subtle"
                color="red"
                type="button"
                onClick={deleteModal.open}
                disabled={submitting}
              >
                Delete
              </Button>
            )}
            <Button type="submit" disabled={!canSubmit} loading={submitting}>
              Save
            </Button>
          </Group>
        </Group>

        <Stack gap="xxlAlt">
          <SectionRow
            title="Set group information"
            description="Used to identify your access control group."
          >
            {/* The group name is its identifier on both the identity side and
                inside the plugin config, and neither endpoint can rename one. */}
            <TextInput
              label="Name"
              placeholder="e.g. engineering-team"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
              required
              autoFocus={!isEdit}
              disabled={isEdit || submitting}
            />
          </SectionRow>

          <SectionRow
            title="Resource Role configuration"
            description="Select which resource roles this group should have access to."
          >
            <ConnectionsMultiSelect value={connectionIds} onChange={setConnectionIds} />
          </SectionRow>

          <SectionRow
            title="Attribute configuration"
            description="Select which attributes this group should have access to."
          >
            <MultiSelect
              label="Attributes"
              placeholder="Select attributes..."
              data={attributeOptions}
              value={attributeNames}
              onChange={setAttributeNames}
              searchable
              clearable
            />
          </SectionRow>
        </Stack>
      </Stack>

      <Modal opened={deleteOpened} onClose={deleteModal.close} title="Delete Group">
        <Stack gap="lg">
          <Text size="sm">
            {`Are you sure you want to delete the group '${groupName}'? This action cannot be undone.`}
          </Text>
          <Group justify="flex-end" gap="sm">
            <Button variant="subtle" color="gray" onClick={deleteModal.close}>
              Cancel
            </Button>
            <Button color="red" onClick={handleDelete} loading={submitting}>
              Delete
            </Button>
          </Group>
        </Stack>
      </Modal>
    </form>
  )
}

// Create and edit share one form: `?group=<name>` switches it to edit mode.
// The name is a query parameter rather than a path segment because the legacy
// CLJS route has the same shape — a path segment would let the old URL fall
// through to the ClojureScript catch-all and render the page twice over.
export default function AccessControlGroupForm() {
  const [searchParams] = useSearchParams()
  const groupName = searchParams.get('group')
  const isEdit = Boolean(groupName)

  const pluginStatus = useAccessControlStore((s) => s.pluginStatus)
  const attributesStatus = useAccessControlStore((s) => s.attributesStatus)
  const fetchPlugin = useAccessControlStore((s) => s.fetchPlugin)
  const fetchAttributes = useAccessControlStore((s) => s.fetchAttributes)

  useEffect(() => {
    fetchPlugin()
    fetchAttributes()
  }, [fetchPlugin, fetchAttributes])

  // Both feed the form's initial state: the plugin holds the group's resource
  // roles, and the attribute list is what the picker diffs against on save.
  const loading =
    pluginStatus === 'idle' ||
    pluginStatus === 'loading' ||
    attributesStatus === 'idle' ||
    attributesStatus === 'loading'

  if (loading) {
    return <PageLoader h={300} />
  }

  // Rendering the form anyway would show empty selections as if the group had
  // no associations, and saving would then wipe the ones that failed to load.
  if (pluginStatus === 'error' || attributesStatus === 'error') {
    return <PageLoader error h={300} message="Failed to load access control." />
  }

  // Nothing to configure until the feature is enabled; the list page owns that
  // decision and shows the activation screen.
  if (pluginStatus === 'not-installed') {
    return <Navigate to={LIST_PATH} replace />
  }

  return (
    <GroupFormFields key={groupName ?? 'new'} groupName={groupName} isEdit={isEdit} />
  )
}
