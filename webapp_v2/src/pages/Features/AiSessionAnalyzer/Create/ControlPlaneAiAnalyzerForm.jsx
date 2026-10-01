import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { ArrowLeft, Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import Modal from '@/components/Modal'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import PageLoader from '@/components/PageLoader'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import TagsInput from '@/components/TagsInput'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { usersService } from '@/services/users'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import {
  analyzerActionsFor,
  operationsFor,
  REVIEW_ACTION,
  REVIEW_MODE_HOLD,
  REVIEW_MODES,
} from '@/pages/sidecarRuleVocabulary'
import { useAiSessionAnalyzerStore } from '../store'
import classes from './Create.module.css'

// The analyzer as a SIDECAR runs it: a per-listener BLOCK, not a rule, and a
// different vocabulary from the gateway's. Its actions are allow, warn, block
// and defer, where the gateway's are allow_execution, block_execution and
// require_access_request; it carries its own trigger and call budget, where
// the gateway's rule carries connection names.
//
// The sidecar's sibling of Create/GatewayAiAnalyzerForm. Neither imports the
// other; Router.jsx picks one with <ByProduct>.
//
// The provider, the model and the credential are NOT here. They are the
// sidecar's top-level analyzer section: a credentials_file is a path on the
// sidecar's filesystem, which a control plane cannot supply, and that section
// is restart-bound where everything on this page hot-swaps.

const EMPTY = {
  trigger_operations: [],
  trigger_tables: [],
  trigger_resources: [],
  high: 'block',
  medium: '',
  low: '',
  prompt: '',
  message: '',
  max_calls: '',
  approval_rule: '',
  review_mode: '',
}

function specToForm(spec) {
  if (!spec) return { ...EMPTY }
  return {
    trigger_operations: spec.trigger?.operations ?? [],
    trigger_tables: spec.trigger?.tables ?? [],
    trigger_resources: spec.trigger?.resources ?? [],
    high: spec.high ?? '',
    medium: spec.medium ?? '',
    low: spec.low ?? '',
    prompt: spec.prompt ?? '',
    message: spec.message ?? '',
    max_calls: spec.max_calls ?? '',
    approval_rule: spec.approval_rule ?? '',
    review_mode: spec.review_mode ?? '',
  }
}

function formToSpec(f, ruleName) {
  const spec = {}
  const trigger = {}
  if (f.trigger_operations.length > 0) trigger.operations = f.trigger_operations
  if (f.trigger_tables.length > 0) trigger.tables = f.trigger_tables
  if (f.trigger_resources.length > 0) trigger.resources = f.trigger_resources
  if (Object.keys(trigger).length > 0) spec.trigger = trigger
  for (const level of ['high', 'medium', 'low']) {
    if (f[level] !== '') spec[level] = f[level]
  }
  if (f.prompt.trim() !== '') spec.prompt = f.prompt.trim()
  if (f.message.trim() !== '') spec.message = f.message.trim()
  if (f.max_calls !== '' && f.max_calls !== null) spec.max_calls = Number(f.max_calls)
  // The rule that says who may release a held statement, named after this one:
  // the control plane owns both halves and keeps them in step, so there is no
  // second name for an operator to get wrong. The sidecar refuses a hold that
  // names nothing, and the plane refuses a review whose rule it cannot find.
  // A stored approval rule that names another rule is an admin's choice of
  // reviewers (an imported file keeps its own), so it stays.
  if ([spec.high, spec.medium, spec.low].includes(REVIEW_ACTION)) {
    spec.approval_rule = f.approval_rule && f.approval_rule !== ruleName ? f.approval_rule : ruleName
    // Only on a lane that holds: the sidecar refuses a mode nothing reads.
    if (f.review_mode !== '') spec.review_mode = f.review_mode
  }
  return spec
}

function FormFields({ rule: stored, ruleName, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)
  const submitting = useAiSessionAnalyzerStore((s) => s.submitting)
  const createRule = useAiSessionAnalyzerStore((s) => s.createRule)
  const updateRule = useAiSessionAnalyzerStore((s) => s.updateRule)
  const deleteRule = useAiSessionAnalyzerStore((s) => s.deleteRule)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const [name, setName] = useState(stored?.name ?? '')
  const [description, setDescription] = useState(stored?.description ?? '')
  const [targets, setTargets] = useState(stored?.sidecar_targets ?? [])
  const [form, setForm] = useState(() => specToForm(stored?.sidecar_spec))
  const set = (patch) => setForm((f) => ({ ...f, ...patch }))
  // Who may release what this rule holds: hoop groups, which each login syncs
  // from the identity provider. Empty leaves it to the administrators.
  const [reviewers, setReviewers] = useState(stored?.reviewers_groups ?? [])
  const [groupOptions, setGroupOptions] = useState([])

  useEffect(() => {
    let cancelled = false
    usersService
      .listGroups()
      .then(({ data }) => {
        if (!cancelled) setGroupOptions(Array.isArray(data) ? data : [])
      })
      // Without the list the field still shows the groups already stored.
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  const protocol = useMemo(() => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    const first = targets[0]
    if (!first) return ''
    const lane = byId.get(first.sidecar_id)?.configuration?.listeners?.find(
      (l) => l.name === first.listener_name,
    )
    return lane?.protocol ?? ''
  }, [targets, sidecars])

  const isHTTP = protocol.toLowerCase() === 'http'
  const operations = useMemo(() => operationsFor(protocol), [protocol])
  const actions = useMemo(() => analyzerActionsFor(), [])
  const noTrigger =
    form.trigger_operations.length === 0 &&
    form.trigger_tables.length === 0 &&
    form.trigger_resources.length === 0

  const canSubmit = name.trim() !== '' && !submitting
  // Every group of the org, plus the stored ones, so a group nobody holds
  // any more still shows and can be removed.
  const reviewerOptions = useMemo(
    () => [...new Set([...groupOptions, ...reviewers])].sort(),
    [groupOptions, reviewers],
  )
  // Reviewers belong to the approval rule this page keeps beside the analyzer
  // rule. A hold that names another rule (an imported file's) keeps that
  // rule's reviewers, so the field is not shown for it.
  const holds = [form.high, form.medium, form.low].includes(REVIEW_ACTION)
  const ownHold = holds && (!form.approval_rule || form.approval_rule === name.trim())

  const handleSave = async () => {
    if (!canSubmit) return
    const spec = formToSpec(form, name.trim())
    if (!spec.high && !spec.medium && !spec.low) {
      showSnackbar({
        level: 'error',
        text: 'Set an action for at least one risk level.',
      })
      return
    }
    const payload = {
      name: name.trim(),
      description: description || null,
      // The gateway's own fields stay empty: a control plane has no
      // connections, and the sidecar's risk vocabulary lives in sidecar_spec.
      connection_names: [],
      risk_evaluation: {
        low_risk_action: 'allow_execution',
        medium_risk_action: 'allow_execution',
        high_risk_action: 'allow_execution',
      },
      agentic: false,
      sidecar_spec: spec,
      sidecar_targets: targets,
      reviewers_groups: ownHold ? reviewers : undefined,
    }
    const { ok, error } = isEdit ? await updateRule(ruleName, payload) : await createRule(payload)
    if (ok) {
      showSnackbar({ level: 'success', text: isEdit ? 'Rule updated.' : 'Rule created.' })
      navigate('/features/ai-session-analyzer')
      return
    }
    showSnackbar({
      level: 'error',
      text: error?.response?.data?.message || 'Failed to save the rule.',
    })
  }

  // The gateway deletes the approval rule this one keeps beside it, so the
  // reviewers of a hold go with the rule.
  const handleDelete = async () => {
    const { ok, error } = await deleteRule(ruleName)
    deleteModal.close()
    if (ok) {
      showSnackbar({ level: 'success', text: 'Rule deleted.' })
      navigate('/features/ai-session-analyzer')
      return
    }
    showSnackbar({
      level: 'error',
      text: error?.response?.data?.message || 'Failed to delete the rule.',
    })
  }

  return (
    <Stack gap={0}>
      <Box>
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() => navigate('/features/ai-session-analyzer')}
          px={0}
          w="fit-content"
          mb="xl"
        >
          Back
        </Button>
      </Box>

      {/* The header of the gateway sibling: pulled up by the shell header's
          height, since useInViewport takes no rootMargin. */}
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
          {isEdit ? 'Edit AI Analyzer rule' : 'Create new AI Analyzer rule'}
        </Title>
        <Group gap="sm">
          {isEdit && (
            <Button variant="subtle" color="red" onClick={deleteModal.open} disabled={submitting}>
              Delete
            </Button>
          )}
          <Button onClick={handleSave} disabled={!canSubmit} loading={submitting}>
            Save
          </Button>
        </Group>
      </Group>

      <Stack gap="xxlAlt">
        <SectionRow
          title="Set rule information"
          description="Used to identify this analysis across the fleet."
        >
          <Stack gap="md">
            <TextInput
              label="Name"
              placeholder="risky-writes"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
              required
              disabled={isEdit}
              description={isEdit ? 'A rule is addressed by name, so it cannot be renamed.' : undefined}
              autoFocus={!isEdit}
            />
            <TextInput
              label="Description (Optional)"
              placeholder="Describe what this watches"
              value={description}
              onChange={(e) => setDescription(e.currentTarget.value)}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="Distribute to listeners"
          description="One rule per listener. The sidecar needs its analyzer section (provider and model) in its config."
        >
          <SidecarTargetPicker value={targets} onChange={setTargets} />
        </SectionRow>

        <SectionRow
          title="What gets classified"
          description="This is the only check that leaves the process and costs money per statement. Narrow it."
          callout={
            <DocsBtnCallOut text="See our docs for triggers and costs" href={docsUrl.sidecar.riskAnalysis} variant="indigo" />
          }
        >
          <Stack gap="md">
            {isHTTP ? (
              <TagsInput
                label="Resources"
                placeholder="/orders/**"
                value={form.trigger_resources}
                onChange={(v) => set({ trigger_resources: v })}
              />
            ) : (
              <>
                <MultiSelect
                  label="Operations"
                  placeholder="Select operations..."
                  data={operations}
                  value={form.trigger_operations}
                  onChange={(v) => set({ trigger_operations: v })}
                  searchable
                  clearable
                />
                <TagsInput
                  label="Tables (optional)"
                  placeholder="customers"
                  value={form.trigger_tables}
                  onChange={(v) => set({ trigger_tables: v })}
                />
              </>
            )}
            {noTrigger && (
              <Alert color="amber" variant="light" icon={<Info size={16} />} radius="md">
                With no trigger, every statement on this listener is sent to the model.
              </Alert>
            )}
            <NumberInput
              label="Call budget (optional)"
              placeholder="Inherit the sidecar’s"
              value={form.max_calls}
              onChange={(v) => set({ max_calls: v })}
              min={0}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="What happens per risk level"
          description="A level you leave unset allows. Hold for approval waits up to 30 minutes for a review; a client that times out first ends the wait, and running it again after approval lets it through. On an SSH listener, drop the shell capability first."
        >
          <Stack gap="md">
            {[
              ['high', 'High risk'],
              ['medium', 'Medium risk'],
              ['low', 'Low risk'],
            ].map(([level, label]) => (
              <Select
                key={level}
                label={label}
                data={actions}
                value={form[level]}
                onChange={(v) => set({ [level]: v ?? '' })}
                allowDeselect={false}
              />
            ))}
            {holds && (
              <Select
                label="While held for approval"
                description="Return denies the statement at once with the review id; after approval, the client must send the identical statement again. A client can also ask for either mode itself."
                data={REVIEW_MODES}
                value={form.review_mode || REVIEW_MODE_HOLD}
                onChange={(v) => set({ review_mode: !v || v === REVIEW_MODE_HOLD ? '' : v })}
                allowDeselect={false}
              />
            )}
            {ownHold && (
              <MultiSelect
                label="Reviewers"
                description="Groups whose members may approve. Empty leaves it to the administrators."
                placeholder="Select groups"
                searchable
                nothingFoundMessage="No user groups defined yet."
                data={reviewerOptions}
                value={reviewers}
                onChange={setReviewers}
              />
            )}
            <TextInput
              label="Denial message (optional)"
              placeholder="refused by risk analysis"
              value={form.message}
              onChange={(e) => set({ message: e.currentTarget.value })}
            />
          </Stack>
        </SectionRow>

        <SectionRow
          title="Custom analysis prompt"
          description="Replaces the default risk guidance for this listener."
        >
          <Textarea
            label="Your prompt (Optional)"
            placeholder="e.g. Treat any statement touching the payments schema as high risk."
            minRows={6}
            maxRows={12}
            value={form.prompt}
            onChange={(e) => set({ prompt: e.currentTarget.value })}
          />
        </SectionRow>
      </Stack>

      <Modal opened={deleteOpened} onClose={deleteModal.close} title="Delete rule">
        <Stack gap="lg">
          <Text size="sm">
            {`Are you sure you want to delete the rule "${ruleName}"? Every listener it is distributed to stops running it. This action cannot be undone.`}
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

export default function ControlPlaneAiAnalyzerForm() {
  const { ruleName } = useParams()
  const isEdit = Boolean(ruleName)

  const active = useAiSessionAnalyzerStore((s) => s.active)
  const activeStatus = useAiSessionAnalyzerStore((s) => s.activeStatus)
  const fetchActive = useAiSessionAnalyzerStore((s) => s.fetchActive)
  const clearActive = useAiSessionAnalyzerStore((s) => s.clearActive)

  useEffect(() => {
    if (isEdit) fetchActive(ruleName)
    return () => clearActive()
  }, [isEdit, ruleName, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }
  if (isEdit && activeStatus === 'error') {
    return <PageLoader error h={400} message="Failed to load rule." />
  }

  return (
    <FormFields
      key={isEdit ? (active?.name ?? ruleName) : 'new'}
      rule={isEdit ? active : null}
      ruleName={ruleName}
      isEdit={isEdit}
    />
  )
}
