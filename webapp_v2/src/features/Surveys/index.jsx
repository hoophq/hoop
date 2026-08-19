import OriginSurvey from '@/features/OriginSurvey'
import TtfvSurvey from '@/features/TtfvSurvey'
import { useUserStore } from '@/stores/useUserStore'

/**
 * Picks the one in-app survey to offer, if any.
 *
 * Every survey renders through SurveyWidget, which anchors to a fixed spot on
 * the right edge of the viewport. Two of them at once would stack two launchers
 * on the same pixels, and the admin of a freshly created org qualifies for both
 * — so the choice is made here rather than by each survey independently.
 *
 * Priority goes to the origin survey because its window is the one that closes:
 * it can only be answered in the first 7 days of a user's life and only once.
 * TTFV survives being deferred — the gateway re-offers it after a cooldown for
 * as long as the org has not confirmed value.
 *
 * The pick is deliberately not frozen. /userinfo is fetched once per app boot
 * (see ProtectedRoute), so answering the origin survey does not flip its flag
 * mid-session: this keeps returning OriginSurvey, which has retired itself and
 * renders nothing. TTFV therefore waits for the next page load instead of
 * popping up seconds after the survey the user just finished.
 */
function Surveys() {
  const showOrigin = useUserStore((state) => !!state.user?.show_origin_survey)
  const showTtfv = useUserStore((state) => !!state.user?.show_ttfv_survey)

  // Each survey re-reads its own flag to drive SurveyWidget's `due`, so that it
  // stays self-contained rather than trusting a caller to have checked.
  if (showOrigin) return <OriginSurvey />
  if (showTtfv) return <TtfvSurvey />
  return null
}

export default Surveys
