import { Stack } from '@mantine/core'
import { useConfigureRoleStore } from '../store'
import ToggleSection from './ToggleSection'
import AIDataMaskingSection from './AIDataMaskingSection'

// Native Access tab: governs whether sessions can be opened against
// this resource role from a native client (desktop app or CLI), plus
// the AI Data Masking pipeline that filters native query results.
//
// The CLJS version also exposes a backward-compat "Just-in-Time Review"
// toggle that's only shown when the connection already has a review
// config. Not ported here — the same gating applies for new
// connections, where the section would render empty. Existing
// review-configured connections fall back to the legacy editor for now.
export default function NativeAccessTab() {
  const drafts = useConfigureRoleStore((s) => s.drafts)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)

  return (
    <Stack gap="xxl" maw={720}>
      <ToggleSection
        title="Native access availability"
        description="Access from your client of preference using hoop.dev to channel resource roles using our Desktop App or our Command Line Interface."
        checked={drafts.access_mode_connect === 'enabled'}
        onChange={(checked) =>
          setDraft({ access_mode_connect: checked ? 'enabled' : 'disabled' })
        }
      />
      <AIDataMaskingSection />
    </Stack>
  )
}
