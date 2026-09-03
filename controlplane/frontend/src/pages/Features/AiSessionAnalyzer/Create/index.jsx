import { useEffect, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { ArrowLeft, Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import ConnectionNamesMultiSelect from '@/components/ConnectionNamesMultiSelect'
import EnterpriseBanner from '@/components/EnterpriseBanner'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import Switch from '@/components/Switch'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { useUserStore } from '@/stores/useUserStore'
import { LICENSE_FEATURE_LABELS, licenseRequiredMessage, licenseState } from '@/utils/license'
import { showSnackbar } from '@/utils/snackbar'
import { useAiSessionAnalyzerStore } from '../store'
import { formToPayload, hasIncompleteTier, riskFromRule } from '../helpers'
import { findAiAnalyzerTemplate } from '../templates'
import SectionRow from '../components/SectionRow'
import RiskEvaluation from './sections/RiskEvaluation'
import SystemPromptPreview from './sections/SystemPromptPreview'
import classes from './Create.module.css'

const LIST_PATH = '/features/ai-session-analyzer'

// Remounted via `key` when the loaded rule changes, so state derives from
// `rule` with lazy useState initializers instead of a prefill effect.
function RuleFormFields({ rule, ruleName, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)

  // Creating a rule needs a valid Enterprise license (hard zero on the client).
  // Editing and deleting the rules that exist stay open in every state, so a
  // deep link to the create route shows the form with Save disabled, not a 403.
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const licenseInfo = useUserStore((s) => s.licenseInfo)
  const blockedByLicense = !isEdit && isFreeLicense
  const licenseMessage = licenseRequiredMessage(
    LICENSE_FEATURE_LABELS['ai-session-analyzer'],
    licenseState(licenseInfo),
  )

  const submitting = useAiSessionAnalyzerStore((s) => s.submitting)
  const accessRequestRules = useAiSessionAnalyzerStore((s) => s.accessRequestRules)
  const accessRequestRulesStatus = useAiSessionAnalyzerStore(
    (s) => s.accessRequestRulesStatus,
  )
  const createRule = useAiSessionAnalyzerStore((s) => s.createRule)
  const updateRule = useAiSessionAnalyzerStore((s) => s.updateRule)
  const deleteRule = useAiSessionAnalyzerStore((s) => s.deleteRule)

  const [form, setForm] = useState(() => ({
    name: rule?.name ?? '',
    description: rule?.description ?? '',
    connectionNames: rule?.connection_names ?? [],
    customPrompt: rule?.custom_prompt ?? '',
    agentic: rule?.agentic ?? false,
  }))
  const [risk, setRisk] = useState(() => riskFromRule(rule))

  const setField = (patch) => setForm((f) => ({ ...f, ...patch }))
  const setTier = (levelKey, tier) => setRisk((r) => ({ ...r, [levelKey]: tier }))

  // An approval gate pointing at no rule would silently never resolve, so the
  // save is blocked until every require_access_request tier names one.
  const incompleteTier = hasIncompleteTier(risk)
  const canSubmit =
    form.name.trim().length > 0 && !incompleteTier && !blockedByLicense && !submitting

  const handleSave = async () => {
    if (!canSubmit) return

    const payload = formToPayload({ ...form, risk })
    // The gateway keys the update on the path segment and never writes `name`,
    // so an edit always addresses the rule it was opened with.
    const { ok, error } = isEdit
      ? await updateRule(ruleName, payload)
      : await createRule(payload)

    if (ok) {
      showSnackbar({
        level: 'success',
        text: isEdit ? 'Rule updated successfully!' : 'Rule created successfully!',
      })
      navigate(LIST_PATH)
    } else {
      showSnackbar({
        level: 'error',
        text: isEdit ? 'Failed to update rule' : 'Failed to create rule',
        description: error?.response?.data?.message || error?.message,
      })
    }
  }

  const handleDelete = async () => {
    const { ok, error } = await deleteRule(ruleName)
    deleteModal.close()
    if (ok) {
      showSnackbar({ level: 'success', text: 'Rule deleted successfully!' })
      navigate(LIST_PATH)
    } else {
      showSnackbar({
        level: 'error',
        text: 'Failed to delete rule',
        description: error?.response?.data?.message || error?.message,
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
          onClick={() => navigate(LIST_PATH)}
          px={0}
          w="fit-content"
          mb="xl"
        >
          Back
        </Button>
      </Box>

      {/* Pulled up by the header's height: useInViewport takes no rootMargin. */}
      <Box
        ref={sentinelRef}
        aria-hidden="true"
        pos="relative"
        top="calc(-1 * var(--app-shell-header-offset, 0rem))"
      />
      <Group
        justify="space-between"
        align="center"
        pos="sticky"
        top="var(--app-shell-header-offset, 0rem)"
        bg="var(--mantine-color-body)"
        py="md"
        mb="xl"
        mx={-PAGE_PADDING}
        px={PAGE_PADDING}
        className={classes.stickyHeader}
        data-scrolled={!headerInView || undefined}
      >
        <Title order={2} lts="-0.00625em">
          {isEdit ? 'Edit AI Session Analyzer rule' : 'Create new AI Session Analyzer rule'}
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

      {blockedByLicense && (
        <Box mb="xl">
          <FreeLicenseCallout message={licenseMessage} />
        </Box>
      )}

      <Stack gap="xxlAlt">
        <SectionRow
          title="Define details"
          description="Used to identify your AI Session Analyzer rule in your resources."
        >
          <Stack gap="md">
            <TextInput
              label="Name"
              placeholder="e.g. Rule-Name-1"
              value={form.name}
              onChange={(e) => setField({ name: e.currentTarget.value })}
              required
              autoFocus={!isEdit}
              // The API addresses rules by name and its update never writes the
              // column, so a rule cannot be renamed after creation.
              disabled={isEdit}
              description={isEdit ? 'A rule name cannot be changed after creation.' : undefined}
            />
            <TextInput
              label="Description (Optional)"
              placeholder="Describe how this rule is used"
              value={form.description}
              onChange={(e) => setField({ description: e.currentTarget.value })}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="Roles configuration"
          description="Select which Resources to apply this configuration."
        >
          <ConnectionNamesMultiSelect
            value={form.connectionNames}
            onChange={(values) => setField({ connectionNames: values })}
          />
        </SectionRow>

        <SectionRow
          title="Custom analysis prompt"
          description="Tell the model what to look for. Hoop appends a system prompt so the model always returns a low/medium/high grade."
        >
          <Stack gap="sm">
            <Textarea
              label="Your prompt (Optional)"
              placeholder="e.g. Treat any query that touches the payments schema as high risk."
              minRows={6}
              maxRows={12}
              value={form.customPrompt}
              onChange={(e) => setField({ customPrompt: e.currentTarget.value })}
            />
            <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
              Hoop prepends a fixed system prompt before your instructions so the
              analyzer always returns a structured low/medium/high grade. This is what
              keeps the actions above reliable.
            </Alert>
            <SystemPromptPreview />
          </Stack>
        </SectionRow>

        <SectionRow
          title="Agentic analysis"
          description="Let the analyzer investigate before grading."
        >
          <Stack gap="sm">
            <Switch
              checked={form.agentic}
              onChange={(e) => setField({ agentic: e.currentTarget.checked })}
              label="Agentic analysis (investigate past sessions & resource metadata before classifying)"
            />
            <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
              {"When enabled, the analyzer runs a tool-calling loop over the user's past sessions and read-only database metadata (query plans, table size, index usage) before assigning a risk level. This is slower but produces a richer, reviewer-facing analysis."}
            </Alert>
          </Stack>
        </SectionRow>

        <RiskEvaluation
          risk={risk}
          onTierChange={setTier}
          accessRequestRules={accessRequestRules}
          rulesFailed={accessRequestRulesStatus === 'error'}
        />
      </Stack>

      <Modal opened={deleteOpened} onClose={deleteModal.close} title="Delete rule">
        <Stack gap="lg">
          <Text size="sm">
            {`Are you sure you want to delete the rule "${ruleName}"? This action cannot be undone.`}
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

export default function AiSessionAnalyzerRuleForm() {
  const { ruleName } = useParams()
  const isEdit = Boolean(ruleName)

  // Activation-journey deep link `?template=&connections=` seeds the form; an
  // unknown template falls back to blank. Carries connection names, not ids.
  const [searchParams] = useSearchParams()
  const template = isEdit ? null : findAiAnalyzerTemplate(searchParams.get('template'))
  const templateConnectionNames = (searchParams.get('connections') ?? '')
    .split(',')
    .filter(Boolean)
  const seededRule = template
    ? { ...template, connection_names: templateConnectionNames }
    : null

  const active = useAiSessionAnalyzerStore((s) => s.active)
  const activeStatus = useAiSessionAnalyzerStore((s) => s.activeStatus)
  const fetchActive = useAiSessionAnalyzerStore((s) => s.fetchActive)
  const clearActive = useAiSessionAnalyzerStore((s) => s.clearActive)
  const fetchAccessRequestRules = useAiSessionAnalyzerStore(
    (s) => s.fetchAccessRequestRules,
  )

  // Without this the post-save redirect lands on the promotion splash instead
  // of the rule just created.
  useEffect(() => {
    if (template) {
      localStorage.setItem('ai-session-analyzer-promotion-seen', 'true')
    }
  }, [template])

  // ConnectionNamesMultiSelect loads/paginates its own options, so no
  // connections fetch here.
  useEffect(() => {
    fetchAccessRequestRules()
    if (isEdit) fetchActive(ruleName)
    return () => clearActive()
  }, [isEdit, ruleName, fetchAccessRequestRules, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }

  // A failed fetch leaves `active` null; rendering the form would present a
  // blank "edit" whose save overwrites the real rule with empty values.
  if (isEdit && activeStatus === 'error') {
    return <PageLoader error h={400} message="Failed to load rule." />
  }

  return (
    <RuleFormFields
      key={isEdit ? (active?.name ?? ruleName) : template ? `template-${template.name}` : 'new'}
      rule={isEdit ? active : seededRule}
      ruleName={ruleName}
      isEdit={isEdit}
    />
  )
}
