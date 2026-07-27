import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import Switch from '@/components/Switch'
import ConnectionsMultiSelect from '@/components/ConnectionsMultiSelect'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { showSnackbar } from '@/utils/snackbar'
import { useUserStore } from '@/stores/useUserStore'
import { useJiraTemplatesStore } from '../store'
import {
  apiTemplateToCmdbRows,
  apiTemplateToMappingRows,
  apiTemplateToPromptRows,
  formToPayload,
  tagsToSelectOptions,
} from '../helpers'
import PresetMappingTable from './sections/PresetMappingTable'
import AutomatedMappingTable from './sections/AutomatedMappingTable'
import PromptsTable from './sections/PromptsTable'
import CmdbTable from './sections/CmdbTable'

const FREE_LICENSE_INFO_MESSAGE =
  'Organizations with Free plan have limited automation. Upgrade to Enterprise to have unlimited access to Jira Templates.'

function FormSection({ title, description, children }) {
  return (
    <Stack gap="lg">
      <Stack gap="xs">
        <Title order={4} fw={600}>
          {title}
        </Title>
        <Text size="sm" c="dimmed">
          {description}
        </Text>
      </Stack>
      {children}
    </Stack>
  )
}

// Remounted via `key` when the loaded template changes, so state derives from
// `template` with lazy useState initializers instead of a prefill effect.
function JiraTemplateFormFields({ template, id, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  const tags = useJiraTemplatesStore((s) => s.tags)
  const submitting = useJiraTemplatesStore((s) => s.submitting)
  const createTemplate = useJiraTemplatesStore((s) => s.createTemplate)
  const updateTemplate = useJiraTemplatesStore((s) => s.updateTemplate)
  const deleteTemplate = useJiraTemplatesStore((s) => s.deleteTemplate)

  const [form, setForm] = useState(() => ({
    name: template?.name ?? '',
    description: template?.description ?? '',
    projectKey: template?.project_key ?? '',
    requestTypeId: template?.request_type_id ?? '',
    issueTransitionNameOnClose: template?.issue_transition_name_on_close ?? '',
    skipTransitionOnNonzeroExitCode: Boolean(
      template?.skip_transition_on_nonzero_exit_code,
    ),
    connectionIds: template?.connection_ids ?? [],
  }))
  const [mappingRows, setMappingRows] = useState(() =>
    apiTemplateToMappingRows(template),
  )
  const [promptRows, setPromptRows] = useState(() =>
    apiTemplateToPromptRows(template),
  )
  const [cmdbRows, setCmdbRows] = useState(() => apiTemplateToCmdbRows(template))

  // The two mapping tables share one select mode, mirroring the legacy form.
  const [mappingSelectMode, setMappingSelectMode] = useState(false)
  const [promptSelectMode, setPromptSelectMode] = useState(false)
  const [cmdbSelectMode, setCmdbSelectMode] = useState(false)

  const setField = (patch) => setForm((f) => ({ ...f, ...patch }))

  const tagOptions = useMemo(() => tagsToSelectOptions(tags), [tags])

  const canSubmit =
    form.name.trim().length > 0 &&
    form.projectKey.trim().length > 0 &&
    form.requestTypeId.trim().length > 0 &&
    !submitting

  const handleSave = async () => {
    if (!canSubmit) return
    const payload = formToPayload({ ...form, mappingRows, promptRows, cmdbRows })
    const { ok, error } = isEdit
      ? await updateTemplate(id, payload)
      : await createTemplate(payload)
    if (ok) {
      showSnackbar({
        level: 'success',
        text: isEdit ? 'Jira template updated.' : 'Jira template created.',
      })
      navigate('/jira-templates')
    } else {
      showSnackbar({
        level: 'error',
        text:
          error?.response?.data?.message ||
          (isEdit ? 'Failed to update Jira template.' : 'Failed to create Jira template.'),
      })
    }
  }

  const handleDelete = async () => {
    const { ok, error } = await deleteTemplate(id)
    deleteModal.close()
    if (ok) {
      showSnackbar({ level: 'success', text: 'Jira template deleted.' })
      navigate('/jira-templates')
    } else {
      showSnackbar({
        level: 'error',
        text: error?.response?.data?.message || 'Failed to delete Jira template.',
      })
    }
  }

  return (
    <Stack gap={0}>
      <Box>
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() => navigate('/jira-templates')}
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
          {isEdit ? 'Configure JIRA Template' : 'Create a new JIRA Template'}
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
          <FreeLicenseCallout message={FREE_LICENSE_INFO_MESSAGE} variant="info" />
        </Box>
      )}

      <Stack gap="xxlAlt">
        <FormSection
          title="Integration details"
          description="Used to identify your Jira configuration in your resource roles."
        >
          <Stack gap="md" maw={600}>
            <TextInput
              label="Name"
              placeholder="e.g. squad-postgresql"
              value={form.name}
              onChange={(e) => setField({ name: e.currentTarget.value })}
              required
              autoFocus
            />
            <TextInput
              label="Description (Optional)"
              placeholder="Describe how this templated will be used."
              value={form.description}
              onChange={(e) => setField({ description: e.currentTarget.value })}
            />
            <TextInput
              label="Project Key"
              placeholder="e.g. PKEY"
              value={form.projectKey}
              onChange={(e) => setField({ projectKey: e.currentTarget.value })}
              required
            />
            <TextInput
              label="Request Type ID"
              placeholder="e.g. 10005"
              value={form.requestTypeId}
              onChange={(e) => setField({ requestTypeId: e.currentTarget.value })}
              required
            />
          </Stack>
        </FormSection>

        <FormSection
          title="Workflow transition status"
          description="Define transition status for Jira cards when commands are executed."
        >
          <Stack gap="lg" maw={600}>
            <TextInput
              label="Status (Optional)"
              placeholder="e.g. qa"
              value={form.issueTransitionNameOnClose}
              onChange={(e) =>
                setField({ issueTransitionNameOnClose: e.currentTarget.value })
              }
              description="This field is case insensitive and uses 'done' status as default."
              inputWrapperOrder={['label', 'input', 'description', 'error']}
            />
            <Switch
              label="Skip transition on failed sessions"
              description="Do not transition the Jira issue when the session finishes with a non-zero exit code."
              checked={form.skipTransitionOnNonzeroExitCode}
              onChange={(e) =>
                setField({ skipTransitionOnNonzeroExitCode: e.currentTarget.checked })
              }
            />
          </Stack>
        </FormSection>

        <FormSection
          title="Associate Resource Roles"
          description="Select resource roles where this template should be applied"
        >
          <Box maw={600}>
            <ConnectionsMultiSelect
              value={form.connectionIds}
              onChange={(values) => setField({ connectionIds: values })}
            />
          </Box>
        </FormSection>

        <FormSection
          title="Configure resource role tags mapping"
          description="Match key-value information in Jira fields with your resource role tags."
        >
          <PresetMappingTable
            rows={mappingRows}
            setRows={setMappingRows}
            selectMode={mappingSelectMode}
            setSelectMode={setMappingSelectMode}
            tagOptions={tagOptions}
            freeLicense={isFreeLicense}
          />
        </FormSection>

        <FormSection
          title="Configure automated mapping"
          description="Append additional information to your Jira cards when executing a command in your resource roles."
        >
          <AutomatedMappingTable
            rows={mappingRows}
            setRows={setMappingRows}
            selectMode={mappingSelectMode}
            setSelectMode={setMappingSelectMode}
            freeLicense={isFreeLicense}
          />
        </FormSection>

        <FormSection
          title="Configure manual prompt"
          description="Request additional information from executed commands."
        >
          <PromptsTable
            rows={promptRows}
            setRows={setPromptRows}
            selectMode={promptSelectMode}
            setSelectMode={setPromptSelectMode}
            freeLicense={isFreeLicense}
          />
        </FormSection>

        <FormSection
          title="Set a configuration management database (CMDB)"
          description="Create an additional layer of relation between CMDBs and hoop services."
        >
          <CmdbTable
            rows={cmdbRows}
            setRows={setCmdbRows}
            selectMode={cmdbSelectMode}
            setSelectMode={setCmdbSelectMode}
            freeLicense={isFreeLicense}
          />
        </FormSection>
      </Stack>

      <Modal opened={deleteOpened} onClose={deleteModal.close} title="Delete Jira template?">
        <Stack gap="lg">
          <Text size="sm">
            This action will permanently delete this Jira template and cannot be
            undone. Are you sure you want to proceed?
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

export default function JiraTemplateForm() {
  const { id } = useParams()
  const isEdit = Boolean(id)

  const active = useJiraTemplatesStore((s) => s.active)
  const activeStatus = useJiraTemplatesStore((s) => s.activeStatus)
  const fetchActive = useJiraTemplatesStore((s) => s.fetchActive)
  const clearActive = useJiraTemplatesStore((s) => s.clearActive)
  const fetchConnectionTags = useJiraTemplatesStore((s) => s.fetchConnectionTags)

  useEffect(() => {
    fetchConnectionTags()
    if (isEdit) fetchActive(id)
    return () => clearActive()
  }, [isEdit, id, fetchConnectionTags, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }

  if (isEdit && activeStatus === 'error') {
    return <Text c="red">Failed to load Jira template.</Text>
  }

  return (
    <JiraTemplateFormFields
      key={isEdit ? (active?.id ?? id) : 'new'}
      template={isEdit ? active : null}
      id={id}
      isEdit={isEdit}
    />
  )
}
