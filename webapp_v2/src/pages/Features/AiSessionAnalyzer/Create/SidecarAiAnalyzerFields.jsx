import { Stack } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import SidecarTargetPicker from '@/components/SidecarTargetPicker'
import TagsInput from '@/components/TagsInput'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { docsUrl } from '@/utils/docsUrl'

// The analyzer as a SIDECAR runs it: a per-listener BLOCK, not a rule, and a
// different vocabulary from the gateway's. Its actions are allow, warn, block
// and defer, where the gateway's are allow_execution, block_execution and
// require_access_request; it carries its own trigger and call budget, where
// the gateway's rule carries connection names.
//
// The provider, the model and the credential are NOT here. They are the
// sidecar's top-level analyzer section: a credentials_file is a path on the
// sidecar's filesystem, which a control plane cannot supply, and that section
// is restart-bound where everything on this form hot-swaps.

const LEVELS = [
  ['high', 'High risk'],
  ['medium', 'Medium risk'],
  ['low', 'Low risk'],
]

/** The fields of a sidecar analyzer rule, driven by useSidecarAiAnalyzerEditor. */
export default function SidecarAiAnalyzerFields({ editor }) {
  const {
    isEdit,
    name,
    setName,
    description,
    setDescription,
    targets,
    setTargets,
    form,
    set,
    reviewers,
    setReviewers,
    reviewerOptions,
    ownHold,
    isHTTP,
    operations,
    actions,
    noTrigger,
  } = editor

  return (
    <Stack gap="xxlAlt">
      <SectionRow title="Set rule information" description="Used to identify this analysis across the fleet.">
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
          <DocsBtnCallOut
            text="See our docs for triggers and costs"
            href={docsUrl.sidecar.riskAnalysis}
            variant="indigo"
          />
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
          {LEVELS.map(([level, label]) => (
            <Select
              key={level}
              label={label}
              data={actions}
              value={form[level]}
              onChange={(v) => set({ [level]: v ?? '' })}
              allowDeselect={false}
            />
          ))}
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

      <SectionRow title="Custom analysis prompt" description="Replaces the default risk guidance for this listener.">
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
  )
}
