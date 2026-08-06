import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Grid, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { ArrowLeft, ClockArrowUp, CodeXml, Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import Modal from '@/components/Modal'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import PageLoader from '@/components/PageLoader'
import Select from '@/components/Select'
import SelectionCard from '@/components/SelectionCard'
import Switch from '@/components/Switch'
import TextInput from '@/components/TextInput'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import { useAccessRequestStore } from '../store'
import { accessTypeLabel, isEligibleForAccessType, sanitizeRuleName } from '../helpers'
import {
  ACCESS_TYPE,
  FREE_LICENSE_MESSAGE,
  LIST_PATH,
  MANAGED_RULE_MESSAGE,
  TIME_RANGE_OPTIONS,
} from '../constants'
import ResourceRolesSelect from '../components/ResourceRolesSelect'
import classes from './Create.module.css'

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

// Remounted via `key` when the edited rule changes, so state derives from the
// loaded rule with lazy useState initializers instead of a prefill effect.
function RuleFormFields({ rule, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)

  const rules = useAccessRequestStore((s) => s.rules)
  const attributes = useAccessRequestStore((s) => s.attributes)
  const userGroups = useAccessRequestStore((s) => s.userGroups)
  const submitting = useAccessRequestStore((s) => s.submitting)
  const createRule = useAccessRequestStore((s) => s.createRule)
  const updateRule = useAccessRequestStore((s) => s.updateRule)
  const deleteRule = useAccessRequestStore((s) => s.deleteRule)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  // Rules that Hoop manages as part of a protection profile: the API accepts
  // changes to approval settings and group lists only, and refuses to delete
  // them, so everything else is locked here.
  const managed = rule?.managed_by != null

  const [name, setName] = useState(() => rule?.name ?? '')
  const [description, setDescription] = useState(() => rule?.description ?? '')
  const [accessType, setAccessType] = useState(
    () => rule?.access_type ?? ACCESS_TYPE.COMMAND,
  )
  // The Select carries strings; the payload parses it back to seconds.
  const [accessDuration, setAccessDuration] = useState(() =>
    rule?.access_max_duration != null ? String(rule.access_max_duration) : null,
  )
  const [connectionNames, setConnectionNames] = useState(
    () => rule?.connection_names ?? [],
  )
  const [attributeNames, setAttributeNames] = useState(() => rule?.attributes ?? [])
  const [approvalRequiredGroups, setApprovalRequiredGroups] = useState(
    () => rule?.approval_required_groups ?? [],
  )
  const [allGroupsMustApprove, setAllGroupsMustApprove] = useState(
    () => rule?.all_groups_must_approve ?? true,
  )
  const [reviewersGroups, setReviewersGroups] = useState(
    () => rule?.reviewers_groups ?? [],
  )
  const [forceApprovalGroups, setForceApprovalGroups] = useState(
    () => rule?.force_approval_groups ?? [],
  )
  const [minApprovals, setMinApprovals] = useState(() => rule?.min_approvals ?? '')

  // Roles already selected may become ineligible under the other access type;
  // confirming the switch is what drops them.
  const [pendingSwitch, setPendingSwitch] = useState(null)

  const connections = usePaginatedConnections({ pageSize: 50 })
  const { ensureLoaded } = connections

  // The picker filters by access-type eligibility and the switch below has to
  // know which of the current selections survive it, both of which need the
  // resource roles loaded — not just once the dropdown is opened.
  useEffect(() => {
    ensureLoaded()
  }, [ensureLoaded])

  const userGroupOptions = userGroups.map((group) => ({ value: group, label: group }))
  const attributeOptions = attributes.map((a) => ({ value: a.name, label: a.name }))

  // The free plan allows a single rule. Editing the one that exists is always
  // allowed; creating a second one is not.
  const canCreate = !isFreeLicense || rules.length < 1
  const blockedByLicense = !isEdit && !canCreate

  // Mirrors the gateway's own validation, so a rule that cannot be saved never
  // costs the admin a round trip: `required` on a Select or a pills input only
  // draws the asterisk — neither field takes part in native form validation.
  const hasTarget = managed || connectionNames.length > 0 || attributeNames.length > 0
  const canSubmit =
    name.trim().length > 0 &&
    hasTarget &&
    reviewersGroups.length > 0 &&
    (accessType !== ACCESS_TYPE.JIT || Boolean(accessDuration)) &&
    (allGroupsMustApprove || Number(minApprovals) >= 1) &&
    !blockedByLicense &&
    !submitting

  const requestAccessTypeSwitch = (target) => {
    if (target === accessType) return
    const byName = new Map(connections.items.map((c) => [c.name, c]))
    const invalid = connectionNames.filter((connectionName) => {
      const connection = byName.get(connectionName)
      return connection && !isEligibleForAccessType(target, connection)
    })
    if (invalid.length === 0) {
      setAccessType(target)
      return
    }
    setPendingSwitch({ target, invalid })
  }

  const confirmAccessTypeSwitch = () => {
    const dropped = new Set(pendingSwitch.invalid)
    setAccessType(pendingSwitch.target)
    setConnectionNames((current) => current.filter((n) => !dropped.has(n)))
    setPendingSwitch(null)
  }

  const handleAllGroupsMustApproveChange = (event) => {
    const checked = event.currentTarget.checked
    setAllGroupsMustApprove(checked)
    // The field becomes required the moment the switch goes off, so it starts
    // at the lowest value that satisfies it rather than empty.
    if (!checked && minApprovals === '') setMinApprovals(1)
  }

  const handleSubmit = async (event) => {
    event.preventDefault()
    if (!canSubmit) return

    const payload = {
      name,
      access_type: accessType,
      // Managed rules target their own resource roles; the API rejects any
      // attempt to retarget them.
      connection_names: managed ? [] : connectionNames,
      approval_required_groups: approvalRequiredGroups,
      all_groups_must_approve: allGroupsMustApprove,
      reviewers_groups: reviewersGroups,
      force_approval_groups: forceApprovalGroups,
      min_approvals: minApprovals === '' ? null : Number(minApprovals),
    }
    // Managed rules must omit attributes — the API requires them absent or
    // byte-identical to what it already stores.
    if (!managed) payload.attributes = attributeNames
    if (description) payload.description = description
    if (accessType === ACCESS_TYPE.JIT && accessDuration) {
      payload.access_max_duration = Number(accessDuration)
    }

    const { ok, error } = isEdit
      ? await updateRule(rule.name, payload)
      : await createRule(payload)

    if (ok) {
      showSnackbar({
        level: 'success',
        text: isEdit
          ? `Access Request rule '${rule.name}' updated successfully!`
          : `Access Request rule '${name}' created successfully!`,
      })
      navigate(LIST_PATH)
      return
    }
    // Covers the OSS ceiling too: the gateway caps rules at one regardless of
    // the free-license flag the client gates on, and answers 403 with its own
    // message.
    showSnackbar({
      level: 'error',
      text: isEdit
        ? 'Failed to update access request rule'
        : 'Failed to create access request rule',
      description: error?.response?.data?.message,
    })
  }

  const handleDelete = async () => {
    const { ok, error } = await deleteRule(rule.name)
    deleteModal.close()
    if (ok) {
      showSnackbar({
        level: 'success',
        text: `Access Request rule '${rule.name}' deleted successfully!`,
      })
      navigate(LIST_PATH)
      return
    }
    showSnackbar({
      level: 'error',
      text: 'Failed to delete access request rule',
      description: error?.response?.data?.message,
    })
  }

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
            {isEdit ? 'Edit Access Request rule' : 'Create new Access Request rule'}
          </Title>
          <Group gap="sm">
            {isEdit && !managed && (
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

        <Stack gap="xl" mb="xl">
          {isFreeLicense && (
            <FreeLicenseCallout
              message={FREE_LICENSE_MESSAGE}
              variant={blockedByLicense ? 'limit' : 'info'}
            />
          )}
          {managed && (
            <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
              {MANAGED_RULE_MESSAGE}
            </Alert>
          )}
        </Stack>

        <Stack gap="xxlAlt" className={classes.form}>
          <SectionRow
            title="Set new rule information"
            description="Used to identify your Access Request rule in your resources."
          >
            <Stack gap="lg">
              {/* The name is the rule's identifier on the gateway and there is
                  no rename path, so editing locks it. */}
              <TextInput
                label="Name"
                placeholder={isEdit ? undefined : 'e.g. data-engineering'}
                value={name}
                onChange={(e) => setName(sanitizeRuleName(e.currentTarget.value))}
                required
                autoFocus={!isEdit}
                disabled={isEdit || submitting}
              />
              <TextInput
                label="Description (Optional)"
                placeholder="Describe how this is used in your resource roles"
                value={description}
                onChange={(e) => setDescription(e.currentTarget.value)}
                disabled={managed || submitting}
              />
            </Stack>
          </SectionRow>

          <SectionRow
            title="Access request type"
            description="Define how to request to your resource roles."
          >
            <Stack gap="md">
              <SelectionCard
                icon={ClockArrowUp}
                title="Just-in-Time"
                description="For temporary access expiring automatically after defined time range"
                selected={accessType === ACCESS_TYPE.JIT}
                disabled={managed}
                onClick={() => requestAccessTypeSwitch(ACCESS_TYPE.JIT)}
              />
              <SelectionCard
                icon={CodeXml}
                title="by Command"
                description="For execution-based with approval workflow"
                selected={accessType === ACCESS_TYPE.COMMAND}
                disabled={managed}
                onClick={() => requestAccessTypeSwitch(ACCESS_TYPE.COMMAND)}
              />
              <Group gap="xs" wrap="nowrap" align="flex-start" c="dimmed">
                <Info size={16} />
                <Text size="sm">
                  Only resource roles that support the selected access type will be
                  available.
                </Text>
              </Group>
            </Stack>
          </SectionRow>

          {accessType === ACCESS_TYPE.JIT && (
            <SectionRow
              title="Access time range"
              description="Select for how long temporary access will be available for your resource roles."
            >
              <Select
                label="Time Range"
                data={TIME_RANGE_OPTIONS}
                value={accessDuration}
                onChange={setAccessDuration}
                required
                clearable
                disabled={managed || submitting}
              />
            </SectionRow>
          )}

          <SectionRow
            title="Resource configuration"
            description="Select which resource roles to apply this configuration."
          >
            <ResourceRolesSelect
              connections={connections}
              accessType={accessType}
              value={connectionNames}
              onChange={setConnectionNames}
              required={!managed && attributeNames.length === 0}
              disabled={managed || submitting}
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
              value={attributeNames}
              onChange={setAttributeNames}
              required={!managed && connectionNames.length === 0}
              disabled={managed || submitting}
              searchable
              clearable
            />
          </SectionRow>

          <SectionRow
            title="User groups requiring review"
            description="Users in these groups must go through an approval review when requesting access with this rule."
          >
            <Stack gap="xs">
              <MultiSelect
                label="User Groups"
                placeholder="Select groups..."
                data={userGroupOptions}
                value={approvalRequiredGroups}
                onChange={setApprovalRequiredGroups}
                disabled={submitting}
                searchable
                clearable
              />
              <Text size="xs" c="dimmed">
                Leave empty to require review from all users.
              </Text>
            </Stack>
          </SectionRow>

          <SectionRow
            title="Approver user groups"
            description="Select which user groups can approve access requests in this rule. Each group counts as one approval."
          >
            <Stack gap="lg">
              <MultiSelect
                label="User Groups"
                placeholder="Select groups..."
                data={userGroupOptions}
                value={reviewersGroups}
                onChange={setReviewersGroups}
                required
                disabled={submitting}
                searchable
                clearable
              />
              <Switch
                size="md"
                checked={allGroupsMustApprove}
                onChange={handleAllGroupsMustApproveChange}
                disabled={submitting}
                label={
                  <Text span size="sm" fw={700}>
                    Require approval from all groups
                  </Text>
                }
                description="At least one member of each group above must approve the request"
              />
            </Stack>
          </SectionRow>

          <SectionRow
            title="Approval amount"
            description="Define the minimum number of groups that must approve each session."
          >
            <NumberInput
              label="Minimum Approval Amount"
              placeholder="e.g. 2"
              min={1}
              value={minApprovals}
              onChange={setMinApprovals}
              required={!allGroupsMustApprove}
              disabled={allGroupsMustApprove || submitting}
            />
          </SectionRow>

          <SectionRow
            title="Force approval groups (Optional)"
            description="Select which user groups are allowed to bypass other approval rules."
          >
            <MultiSelect
              label="User Groups"
              placeholder="Select groups..."
              data={userGroupOptions}
              value={forceApprovalGroups}
              onChange={setForceApprovalGroups}
              disabled={submitting}
              searchable
              clearable
            />
          </SectionRow>
        </Stack>
      </Stack>

      <Modal
        opened={pendingSwitch != null}
        onClose={() => setPendingSwitch(null)}
        title="Change access type?"
      >
        {/* Rendered from the pending switch, so the copy is dropped rather than
            read as `undefined` while the modal fades out. */}
        {pendingSwitch && (
          <Stack gap="lg">
            <Text size="sm">
              {`Switching to ${accessTypeLabel(pendingSwitch.target)} will remove ${pendingSwitch.invalid.length} resource roles that don't support this type. This can't be undone.`}
            </Text>
            <Group justify="flex-end" gap="sm">
              <Button
                variant="subtle"
                color="gray"
                onClick={() => setPendingSwitch(null)}
              >
                Cancel
              </Button>
              <Button onClick={confirmAccessTypeSwitch}>Confirm</Button>
            </Group>
          </Stack>
        )}
      </Modal>

      <Modal opened={deleteOpened} onClose={deleteModal.close} title="Delete Rule">
        <Stack gap="lg">
          <Text size="sm">
            {`Are you sure you want to delete the rule '${rule?.name}'? This action cannot be undone.`}
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

// Create and edit share one form; the rule name is a path segment so the
// legacy `/features/access-request/edit/:rule-name` links keep working.
export default function AccessRequestRuleForm() {
  const { ruleName } = useParams()
  const isEdit = Boolean(ruleName)

  const rule = useAccessRequestStore((s) => s.rule)
  const ruleStatus = useAccessRequestStore((s) => s.ruleStatus)
  const rulesStatus = useAccessRequestStore((s) => s.rulesStatus)
  const attributesStatus = useAccessRequestStore((s) => s.attributesStatus)
  const userGroupsStatus = useAccessRequestStore((s) => s.userGroupsStatus)
  const fetchRule = useAccessRequestStore((s) => s.fetchRule)
  const fetchRules = useAccessRequestStore((s) => s.fetchRules)
  const fetchAttributes = useAccessRequestStore((s) => s.fetchAttributes)
  const fetchUserGroups = useAccessRequestStore((s) => s.fetchUserGroups)
  const clearRule = useAccessRequestStore((s) => s.clearRule)

  useEffect(() => {
    // The rule list is what the free-plan gate counts, so the form needs it
    // even when the admin lands here from a bookmark.
    fetchRules()
    fetchAttributes()
    fetchUserGroups()
  }, [fetchRules, fetchAttributes, fetchUserGroups])

  useEffect(() => {
    if (!ruleName) return undefined
    fetchRule(ruleName)
    return clearRule
  }, [ruleName, fetchRule, clearRule])

  const pending = (status) => status === 'idle' || status === 'loading'
  const loading =
    pending(rulesStatus) ||
    pending(attributesStatus) ||
    pending(userGroupsStatus) ||
    (isEdit && pending(ruleStatus))

  if (loading) {
    return <PageLoader h={300} />
  }

  // Rendering the form anyway would show empty pickers as if the rule targeted
  // nothing, and saving would then wipe whatever failed to load.
  if (
    rulesStatus === 'error' ||
    attributesStatus === 'error' ||
    userGroupsStatus === 'error' ||
    (isEdit && ruleStatus === 'error')
  ) {
    return <PageLoader error h={300} message="Failed to load access request rule." />
  }

  return (
    <RuleFormFields key={ruleName ?? 'new'} rule={isEdit ? rule : null} isEdit={isEdit} />
  )
}
