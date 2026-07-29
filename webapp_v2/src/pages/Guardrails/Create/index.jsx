import { useEffect, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Box, Grid, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { ArrowLeft } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import ConnectionsMultiSelect from '@/components/ConnectionsMultiSelect'
import EnterpriseBanner from '@/components/EnterpriseBanner'
import Modal from '@/components/Modal'
import MultiSelect from '@/components/MultiSelect'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import { useGuardrailsStore } from '../store'
import { apiRulesToRows, formToPayload, orphanMessageError } from '../helpers'
import { findGuardrailTemplate } from '../templates'
import RulesTable from './components/RulesTable'

function SectionRow({ title, badge, description, children }) {
  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Group gap="xs" align="center">
            <Title order={4} fw={500}>
              {title}
            </Title>
            {badge}
          </Group>
          <Text size="sm" c="dimmed">
            {description}
          </Text>
        </Stack>
      </Grid.Col>
      <Grid.Col span={5}>{children}</Grid.Col>
    </Grid>
  )
}

// Remounted via `key` when the loaded guardrail changes, so state derives from
// `guardrail` with lazy useState initializers instead of a prefill effect.
function GuardrailFormFields({ guardrail, id, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  const attributes = useGuardrailsStore((s) => s.attributes)
  const submitting = useGuardrailsStore((s) => s.submitting)
  const createGuardrail = useGuardrailsStore((s) => s.createGuardrail)
  const updateGuardrail = useGuardrailsStore((s) => s.updateGuardrail)
  const deleteGuardrail = useGuardrailsStore((s) => s.deleteGuardrail)

  const [form, setForm] = useState(() => ({
    name: guardrail?.name ?? '',
    description: guardrail?.description ?? '',
    connectionIds: guardrail?.connection_ids ?? [],
    attributes: guardrail?.attributes ?? [],
  }))
  const [inputRules, setInputRules] = useState(() => apiRulesToRows(guardrail?.input))
  const [outputRules, setOutputRules] = useState(() => apiRulesToRows(guardrail?.output))
  const [inputSelectMode, setInputSelectMode] = useState(false)
  const [outputSelectMode, setOutputSelectMode] = useState(false)

  const setField = (patch) => setForm((f) => ({ ...f, ...patch }))

  const canSubmit = form.name.trim().length > 0 && !submitting

  const handleSave = async () => {
    if (!canSubmit) return

    // Rows carrying only a message are dropped by formToPayload, which would
    // silently discard what the admin typed — block the save instead.
    const orphanError = orphanMessageError(inputRules, outputRules)
    if (orphanError) {
      showSnackbar({ level: 'error', text: orphanError })
      return
    }

    const payload = formToPayload({
      id: isEdit ? id : '',
      name: form.name.trim(),
      description: form.description,
      connectionIds: form.connectionIds,
      attributes: form.attributes,
      inputRules,
      outputRules,
    })
    const { ok, error } = isEdit
      ? await updateGuardrail(id, payload)
      : await createGuardrail(payload)
    if (ok) {
      showSnackbar({
        level: 'success',
        text: isEdit ? 'Guardrail updated.' : 'Guardrail created.',
      })
      navigate('/guardrails')
    } else {
      showSnackbar({
        level: 'error',
        text:
          error?.response?.data?.message ||
          (isEdit ? 'Failed to update guardrail.' : 'Failed to create guardrail.'),
      })
    }
  }

  const handleDelete = async () => {
    const { ok, error } = await deleteGuardrail(id)
    deleteModal.close()
    if (ok) {
      showSnackbar({ level: 'success', text: 'Guardrail deleted.' })
      navigate('/guardrails')
    } else {
      showSnackbar({
        level: 'error',
        text: error?.response?.data?.message || 'Failed to delete guardrail.',
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
          onClick={() => navigate('/guardrails')}
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
          {isEdit ? 'Configure Guardrail' : 'Create a new Guardrail'}
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
          <EnterpriseBanner />
        </Box>
      )}

      <Stack gap="xxlAlt">
        <SectionRow
          title="Set Guardrail information"
          description="Used to identify your Guardrail in your resource roles."
        >
          <Stack gap="md">
            <TextInput
              label="Name"
              placeholder="Sensitive Data"
              value={form.name}
              onChange={(e) => setField({ name: e.currentTarget.value })}
              required
              autoFocus
            />
            <TextInput
              label="Description (Optional)"
              placeholder="Describe how this is used in your resource roles"
              value={form.description}
              onChange={(e) => setField({ description: e.currentTarget.value })}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="Associate Resource Roles"
          description="Select the resource roles where this guardrail should be applied."
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

        <SectionRow
          title="Configure rules"
          badge={
            <Badge variant="active" size="xs">
              Beta
            </Badge>
          }
          description="Setup rules with Presets or Custom regular expression scripts."
        >
          <Stack gap="lgAlt">
            <RulesTable
              title="Input rules"
              rules={inputRules}
              setRules={setInputRules}
              selectMode={inputSelectMode}
              setSelectMode={setInputSelectMode}
              freeLicense={isFreeLicense}
            />
            <RulesTable
              title="Output rules"
              rules={outputRules}
              setRules={setOutputRules}
              selectMode={outputSelectMode}
              setSelectMode={setOutputSelectMode}
              freeLicense={isFreeLicense}
            />
          </Stack>
        </SectionRow>
      </Stack>

      <Modal opened={deleteOpened} onClose={deleteModal.close} title="Delete Guardrail?">
        <Stack gap="lg">
          <Text size="sm">
            This action will permanently delete this Guardrail and cannot be undone. Are
            you sure you want to proceed?
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

export default function GuardrailForm() {
  const { id } = useParams()
  const isEdit = Boolean(id)

  // Activation-journey deep link: /guardrails/new?template=<name>&connections=<ids>
  // pre-applies a recommended guardrail. An unknown or stale template name
  // falls back to the regular blank form. The URL is the source of truth, so a
  // browser refresh re-seeds the same template. No free-plan clamp is needed:
  // every template stays within the one-rule-per-table OSS limit.
  const [searchParams] = useSearchParams()
  const template = isEdit ? null : findGuardrailTemplate(searchParams.get('template'))
  const templateConnectionIds = (searchParams.get('connections') ?? '')
    .split(',')
    .filter(Boolean)
  const seededGuardrail = template
    ? { ...template, connection_ids: templateConnectionIds }
    : null

  const active = useGuardrailsStore((s) => s.active)
  const activeStatus = useGuardrailsStore((s) => s.activeStatus)
  const fetchActive = useGuardrailsStore((s) => s.fetchActive)
  const clearActive = useGuardrailsStore((s) => s.clearActive)
  const fetchAttributes = useGuardrailsStore((s) => s.fetchAttributes)

  // ConnectionsMultiSelect loads/paginates its own options, so no connections fetch here.
  useEffect(() => {
    fetchAttributes()
    if (isEdit) fetchActive(id)
    return () => clearActive()
  }, [isEdit, id, fetchAttributes, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }

  // A failed fetch leaves `active` null; rendering the form would present a
  // blank "edit" whose save overwrites the real guardrail with empty rules.
  // Block it like the loading state.
  if (isEdit && activeStatus === 'error') {
    return <Text c="red">Failed to load guardrail.</Text>
  }

  return (
    <GuardrailFormFields
      key={isEdit ? (active?.id ?? id) : template ? `template-${template.name}` : 'new'}
      guardrail={isEdit ? active : seededGuardrail}
      id={id}
      isEdit={isEdit}
    />
  )
}
