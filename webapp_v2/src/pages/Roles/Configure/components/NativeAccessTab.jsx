import { Stack } from '@mantine/core'
import { useConfigureRoleStore } from '../store'
import ToggleSection from './ToggleSection'
import AIDataMaskingSection from './AIDataMaskingSection'
import ReviewSection from './ReviewSection'
import { hasReviewConfig } from '../utils/reviewConfig'

// Native Access tab: governs whether sessions can be opened against
// this resource role from a native client (desktop app or CLI), plus
// the AI Data Masking pipeline that filters native query results.
//
// The backward-compat "Just-in-Time Review" section appears only when
// the loaded connection already carries a review config — same gating
// as CLJS native_access_tab.cljs.
export default function NativeAccessTab({ connection }) {
  const drafts = useConfigureRoleStore((s) => s.drafts)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)
  const showReview = hasReviewConfig(connection)

  return (
    <Stack gap="xl" maw={720}>
      <ToggleSection
        title="Native access availability"
        description="Access from your client of preference using hoop.dev to channel resource roles using our Desktop App or our Command Line Interface."
        checked={drafts.access_mode_connect === 'enabled'}
        onChange={(checked) =>
          setDraft({ access_mode_connect: checked ? 'enabled' : 'disabled' })
        }
      />
      {showReview && <ReviewSection kind="jit" />}
      <AIDataMaskingSection />
    </Stack>
  )
}
