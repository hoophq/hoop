import { Combine, FolderLock, SlidersHorizontal } from 'lucide-react'
import FeaturePromotion from '@/components/FeaturePromotion'
import { docsUrl } from '@/utils/docsUrl'
import { RULE_DRIVEN_PROVIDERS } from '../helpers'

// Empty-state behavior is gated by the server's DLP `redact_provider`:
//   - mspresidio / alcatraz → "Create new rule" CTA into the create flow, since
//     both drive masking from data-masking rules.
//   - gcp → docs link + deprecated-provider warning, no create path.
//   - unset → docs link only; there is no provider to call deprecated.
// providerRequired=false (sidecar rules) skips the gate: sidecars mask
// in-process and need no DLP provider.
const FEATURE_ITEMS = [
  {
    icon: <FolderLock size={20} />,
    title: 'No Configuration Required',
    description:
      'Automatically masks sensitive data in the data stream of any Resource where Data Masking is enabled.',
  },
  {
    icon: <Combine size={20} />,
    title: 'Real-Time Protection',
    description:
      'Sensitive data is masked in real-time, ensuring that no unprotected data is exposed during access sessions.',
  },
  {
    icon: <SlidersHorizontal size={20} />,
    title: 'Customizable Setup',
    description:
      'Easily add or remove fields to tailor the masking setup to your specific needs.',
  },
]

const DEPRECATED_GCP_INFO =
  'Your organization has a deprecated Google Cloud DLP configuration. Check our Microsoft Presidio documentation to enable an upgraded version of Data Masking setup in your environment.'

export default function DataMaskingPromotion({
  redactProvider,
  providerRequired = true,
  onConfigure,
}) {
  const canConfigure =
    !providerRequired || RULE_DRIVEN_PROVIDERS.includes(redactProvider)
  const providerProps = canConfigure
    ? {
        onPrimaryClick: onConfigure,
        primaryText: 'Create new rule',
      }
    : {
        docsHref: docsUrl.features.aiDatamasking,
        docsText: 'Read the Data Masking docs',
        extraInformation:
          redactProvider === 'gcp' ? DEPRECATED_GCP_INFO : undefined,
      }

  return (
    <FeaturePromotion
      featureName="Data Masking"
      mode="empty-state"
      image="data-masking-promotion.png"
      description="Data Masking rules that mask sensitive data in real time at the protocol layer."
      featureItems={FEATURE_ITEMS}
      {...providerProps}
    />
  )
}
