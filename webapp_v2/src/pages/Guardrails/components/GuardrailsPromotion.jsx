import { ListCheck, ShieldCheck, TextSearch } from 'lucide-react'
import FeaturePromotion from '@/components/FeaturePromotion'

// Guardrails need no DLP provider: the agent (or sidecar) enforces them with
// its own pattern matching, see gateway/services/providers.go.
const FEATURE_ITEMS = [
  {
    icon: <ListCheck size={20} />,
    title: 'Automated Policy Enforcement',
    description:
      "Real-time monitoring of access policies, automatic detection and prevention of risky operations with customizable rules based on your organization's security requirements.",
  },
  {
    icon: <ShieldCheck size={20} />,
    title: 'Smart Command Filtering',
    description:
      'Block potentially dangerous commands before execution and prevent accidental data modifications or deletions.',
  },
  {
    icon: <TextSearch size={20} />,
    title: 'Context-Aware Access',
    description:
      'Evaluate access requests based on user context, consider factors like time, location, and previous activity and create an adaptive security measurement based on risk assessment.',
  },
]

export default function GuardrailsPromotion({ onCreate }) {
  return (
    <FeaturePromotion
      featureName="Guardrails"
      mode="empty-state"
      image="guardrails-promotion.png"
      description="Create custom rules to guide and protect usage within your resource roles."
      featureItems={FEATURE_ITEMS}
      onPrimaryClick={onCreate}
      primaryText="Create new Guardrails"
    />
  )
}
