import { Stack, Anchor } from '@mantine/core'
import { ArrowUpRight, Star } from 'lucide-react'
import Alert from '@/components/Alert'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import Select from '@/components/Select'
import { useUserStore } from '@/stores/useUserStore'
import { useConfigureRoleStore } from '../store'
import ToggleSection from './ToggleSection'

// Backward-compatible Review section, rendered only when the connection
// already carries review config on load. New connections use the
// dedicated Access Request flow; we don't expose this control there.
//
// `kind` selects the labelling — "command" for the Terminal Access tab
// (Review by Command) and "jit" for the Native Access tab
// (Just-in-Time Review). Both share the same controls and the same
// underlying connection fields.

const TIME_RANGE_OPTIONS = [
  { value: 900, label: '15 minutes' },
  { value: 1800, label: '30 minutes' },
  { value: 3600, label: '1 hour' },
  { value: 7200, label: '2 hours' },
  { value: 14400, label: '4 hours' },
  { value: 28800, label: '8 hours' },
]

export default function ReviewSection({ kind = 'command' }) {
  const drafts = useConfigureRoleStore((s) => s.drafts)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)
  const userGroupsList = useConfigureRoleStore((s) => s.userGroupsList)
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  const groupOptions = (userGroupsList || []).map((g) => ({ value: g, label: g }))
  const reviewEnabled = drafts.reviewers.length > 0

  const handleReviewToggle = (enabled) => {
    if (enabled) {
      // Surfacing the toggle without any approval groups would be invalid
      // — seed with the first available group so the section is usable.
      if (drafts.reviewers.length === 0 && userGroupsList.length > 0) {
        setDraft({ reviewers: [userGroupsList[0]] })
      }
    } else {
      setDraft({
        reviewers: [],
        min_review_approvals: null,
        force_approve_groups: [],
        access_max_duration: kind === 'jit' ? null : drafts.access_max_duration,
      })
    }
  }

  const title = kind === 'jit' ? 'Just-in-Time Review' : 'Review by Command'

  return (
    <ToggleSection
      title={title}
      description="Require approval prior to resource role execution."
      checked={reviewEnabled}
      disabled={isFreeLicense}
      onChange={handleReviewToggle}
      complement={
        reviewEnabled && (
          <Stack gap="md" mt="sm">
            <MultiSelect
              label="Approval user groups"
              placeholder="Select groups"
              searchable
              nothingFoundMessage="No user groups defined yet."
              data={groupOptions}
              value={drafts.reviewers}
              onChange={(value) => setDraft({ reviewers: value })}
              required
              disabled={isFreeLicense}
            />
            <NumberInput
              label="Minimum approval amount (optional)"
              placeholder="e.g. 2"
              value={drafts.min_review_approvals ?? ''}
              onChange={(value) =>
                setDraft({
                  min_review_approvals: typeof value === 'number' ? value : null,
                })
              }
              min={1}
              disabled={isFreeLicense}
            />
            <MultiSelect
              label="Force approval groups (optional)"
              placeholder="Select groups"
              searchable
              data={groupOptions}
              value={drafts.force_approve_groups}
              onChange={(value) => setDraft({ force_approve_groups: value })}
              disabled={isFreeLicense}
            />
            {kind === 'jit' && (
              <Select
                label="Time Range"
                placeholder="Select duration"
                clearable
                data={TIME_RANGE_OPTIONS.map((o) => ({
                  value: String(o.value),
                  label: o.label,
                }))}
                value={
                  drafts.access_max_duration != null
                    ? String(drafts.access_max_duration)
                    : null
                }
                onChange={(value) =>
                  setDraft({
                    access_max_duration: value ? parseInt(value, 10) : null,
                  })
                }
                disabled={isFreeLicense}
              />
            )}
          </Stack>
        )
      }
      learnMore={
        <Stack gap="xs" align="flex-start">
          {isFreeLicense && (
            <Alert variant="light" color="indigo" icon={<Star size={16} />} w="100%">
              {kind === 'jit'
                ? 'Enable Just-in-Time Reviews by upgrading your plan.'
                : 'Enable Review by Command by upgrading your plan.'}
            </Alert>
          )}
          <Anchor
            size="sm"
            href="https://hoop.dev/docs/features/jit-reviews"
            target="_blank"
            rel="noopener noreferrer"
            display="inline-flex"
          >
            <ArrowUpRight size={14} />
            {kind === 'jit' ? 'Learn more about Just-in-Time Reviews' : 'Learn more about Reviews'}
          </Anchor>
        </Stack>
      }
    />
  )
}
