import { useEffect, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Box, Grid, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import Textarea from '@/components/Textarea'
import MultiSelect from '@/components/MultiSelect'
import ConnectionsMultiSelect from '@/components/ConnectionsMultiSelect'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { useUserStore } from '@/stores/useUserStore'
import { useDataMaskingStore } from '../store'
import { apiRuleToFormRows, createEmptyRow, formToPayload, scoreToPercent } from '../helpers'
import { findMaskingTemplate } from '../templates'
import RulesTable from './components/RulesTable'

const FREE_LICENSE_MESSAGE =
  'Organizations with Free plan have limited data protection. Upgrade to Enterprise to have unlimited access to Live Data Masking.'

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

// Remounted via `key` when the loaded rule changes, so state derives from `rule`
// with lazy useState initializers instead of a prefill effect.
function DataMaskingFormFields({ rule, id, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  const attributes = useDataMaskingStore((s) => s.attributes)
  const submitting = useDataMaskingStore((s) => s.submitting)
  const createRule = useDataMaskingStore((s) => s.createRule)
  const updateRule = useDataMaskingStore((s) => s.updateRule)
  const deleteRule = useDataMaskingStore((s) => s.deleteRule)

  const [form, setForm] = useState(() => ({
    name: rule?.name ?? '',
    description: rule?.description ?? '',
    scoreThreshold: scoreToPercent(rule?.score_threshold),
    connectionIds: rule?.connection_ids ?? [],
    attributes: rule?.attributes ?? [],
  }))
  const [rules, setRules] = useState(() =>
    rule ? apiRuleToFormRows(rule) : [createEmptyRow()],
  )
  const [selectMode, setSelectMode] = useState(false)

  const setField = (patch) => setForm((f) => ({ ...f, ...patch }))

  const canSubmit = form.name.trim().length > 0 && !submitting

  const handleSave = async () => {
    if (!canSubmit) return
    const payload = formToPayload({ ...form, name: form.name.trim(), rules })
    const { ok, error } = isEdit
      ? await updateRule(id, payload)
      : await createRule(payload)
    if (ok) {
      notifications.show({
        message: isEdit ? 'Rule updated.' : 'Rule created.',
        color: 'green',
      })
      navigate('/features/data-masking')
    } else {
      notifications.show({
        message:
          error?.response?.data?.message ||
          (isEdit ? 'Failed to update rule.' : 'Failed to create rule.'),
        color: 'red',
      })
    }
  }

  const handleDelete = async () => {
    const { ok, error } = await deleteRule(id)
    deleteModal.close()
    if (ok) {
      notifications.show({ message: 'Rule deleted.', color: 'green' })
      navigate('/features/data-masking')
    } else {
      notifications.show({
        message: error?.response?.data?.message || 'Failed to delete rule.',
        color: 'red',
      })
    }
  }

  const attributeOptions = attributes.map((a) => ({
    value: a.name,
    label: a.name,
  }))

  return (
    <Stack gap={0}>
      <Box>
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() => navigate('/features/data-masking')}
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
        mb="xl"
        mx={-PAGE_PADDING}
        px={PAGE_PADDING}
        style={{
          zIndex: 10,
          borderBottom: headerInView
            ? '1px solid transparent'
            : '1px solid var(--mantine-color-default-border)',
        }}
      >
        <Title order={2}>
          {isEdit ? 'Edit Live Data Masking rule' : 'Create new Live Data Masking rule'}
        </Title>
        <Group gap="sm">
          {isEdit && (
            <Button
              variant="subtle"
              color="red"
              onClick={deleteModal.open}
              disabled={submitting}
            >
              Delete
            </Button>
          )}
          <Button onClick={handleSave} disabled={!canSubmit} loading={submitting}>
            Save
          </Button>
        </Group>
      </Group>

      {isFreeLicense && (
        <Box mb="xl">
          <FreeLicenseCallout message={FREE_LICENSE_MESSAGE} />
        </Box>
      )}

      <Stack gap="xxlAlt">
        <SectionRow
          title="Set rule information"
          description="Used to identify the rule in your resource roles."
        >
          <Stack gap="md">
            <TextInput
              label="Name"
              placeholder="e.g. Sensitive Data"
              value={form.name}
              onChange={(e) => setField({ name: e.currentTarget.value })}
              required
              autoFocus
            />
            <Textarea
              label="Description (Optional)"
              placeholder="Describe how this is used in your resource roles"
              value={form.description}
              onChange={(e) => setField({ description: e.currentTarget.value })}
              minRows={3}
            />
            <TextInput
              label="Analyzer confidence threshold (Optional)"
              type="number"
              min={1}
              max={100}
              placeholder="85"
              value={form.scoreThreshold}
              onChange={(e) => {
                const value = e.currentTarget.value
                setField({ scoreThreshold: value === '' ? '' : Number(value) })
              }}
              description="Minimum confidence level required to detect and mask sensitive data. Default 85% works well for most use cases."
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="Resource Role configuration"
          description="Select which resource roles to apply this configuration."
        >
          <ConnectionsMultiSelect
            value={form.connectionIds}
            onChange={(values) => setField({ connectionIds: values })}
          />
        </SectionRow>

        <SectionRow
          title="Attribute configuration"
          description="Select which Attributes to apply this configuration."
        >
          <MultiSelect
            label="Attributes"
            placeholder="Select attributes..."
            data={attributeOptions}
            value={form.attributes}
            onChange={(values) => setField({ attributes: values })}
            searchable
            clearable
          />
        </SectionRow>

        <Stack gap="md">
          <Title order={4} fw={500}>
            Output rules
          </Title>
          <RulesTable
            rules={rules}
            setRules={setRules}
            selectMode={selectMode}
            setSelectMode={setSelectMode}
            freeLicense={isFreeLicense}
          />
        </Stack>
      </Stack>

      <Modal
        opened={deleteOpened}
        onClose={deleteModal.close}
        title="Delete Live Data Masking rule?"
      >
        <Stack gap="lg">
          <Text size="sm">
            This action will permanently delete this Live Data Masking rule and cannot
            be undone. Are you sure you want to proceed?
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
    </Stack>
  )
}

export default function DataMaskingForm() {
  const { id } = useParams()
  const isEdit = Boolean(id)

  // Activation-journey deep link: /features/data-masking/new?template=<id>
  // pre-applies a recommended template. An unknown or stale template id
  // falls back to the regular blank form. The URL is the source of truth,
  // so a browser refresh re-seeds the same template.
  const [searchParams] = useSearchParams()
  const template = isEdit ? null : findMaskingTemplate(searchParams.get('template'))
  const templateConnectionIds = (searchParams.get('connections') ?? '')
    .split(',')
    .filter(Boolean)
  const templateRule = template
    ? { ...template.rule, connection_ids: templateConnectionIds }
    : null

  const active = useDataMaskingStore((s) => s.active)
  const activeStatus = useDataMaskingStore((s) => s.activeStatus)
  const fetchActive = useDataMaskingStore((s) => s.fetchActive)
  const clearActive = useDataMaskingStore((s) => s.clearActive)
  const fetchAttributes = useDataMaskingStore((s) => s.fetchAttributes)

  // ConnectionsMultiSelect loads/paginates its own options, so no connections fetch here.
  useEffect(() => {
    fetchAttributes()
    if (isEdit) fetchActive(id)
    return () => clearActive()
  }, [isEdit, id, fetchAttributes, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }

  return (
    <DataMaskingFormFields
      key={isEdit ? (active?.id ?? id) : template ? `template-${template.id}` : 'new'}
      rule={isEdit ? active : templateRule}
      id={id}
      isEdit={isEdit}
    />
  )
}
