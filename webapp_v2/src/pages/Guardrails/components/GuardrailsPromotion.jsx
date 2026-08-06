import { ListCheck, ShieldCheck, TextSearch } from 'lucide-react'
import FeaturePromotion from '@/components/FeaturePromotion'
import { docsUrl } from '@/utils/docsUrl'

// Guardrails are enforced through a DLP provider (GCP or Microsoft Presidio),
// so without one the feature cannot be set up: the panel then explains the
// requirement and links to the docs instead of offering the create CTA.
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

const DLP_REQUIRED_INFO =
  'Guardrails require a DLP provider (Microsoft Presidio or Google Cloud DLP) to be enforced. Configure a DLP provider to create and manage guardrails.'

export default function GuardrailsPromotion({ dlpAvailable, onCreate }) {
  const providerProps = dlpAvailable
    ? { onPrimaryClick: onCreate, primaryText: 'Create new Guardrails' }
    : {
        docsHref: docsUrl.features.guardrails,
        docsText: 'Go to Guardrails documentation',
        extraInformation: DLP_REQUIRED_INFO,
      }

  return (
    <FeaturePromotion
      featureName="Guardrails"
      mode="empty-state"
      image="guardrails-promotion.png"
      description="Create custom rules to guide and protect usage within your resource roles."
      featureItems={FEATURE_ITEMS}
      {...providerProps}
    />
  )
}
