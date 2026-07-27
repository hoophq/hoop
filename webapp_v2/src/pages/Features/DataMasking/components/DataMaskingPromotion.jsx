import { Combine, FolderLock, SlidersHorizontal } from 'lucide-react'
import FeaturePromotion from '@/components/FeaturePromotion'
import { docsUrl } from '@/utils/docsUrl'

// Empty-state behavior is gated by the gateway's DLP `redact_provider`:
//   - mspresidio → "Configure" CTA into the create flow.
//   - gcp / unset → docs link + deprecated-provider warning, no create path.
const FEATURE_ITEMS = [
  {
    icon: <FolderLock size={20} />,
    title: 'No Configuration Required',
    description:
      'Automatically masks sensitive data in the data stream of any connection where Live Data Masking is enabled.',
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
  'Your organization has a deprecated Google Cloud DLP configuration. Check our Microsoft Presidio documentation to enable an upgraded version of Live Data Masking setup in your environment.'

export default function DataMaskingPromotion({ redactProvider, onConfigure }) {
  const providerProps =
    redactProvider === 'mspresidio'
      ? {
          onPrimaryClick: onConfigure,
          primaryText: 'Configure Live Data Masking',
        }
      : {
          docsHref: docsUrl.features.aiDatamasking,
          docsText: 'Go to Live Data Masking Docs',
          extraInformation: DEPRECATED_GCP_INFO,
        }

  return (
    <FeaturePromotion
      featureName="Live Data Masking"
      mode="empty-state"
      image="data-masking-promotion.png"
      description="Zero-config DLP policies that automatically mask sensitive data in real-time at the protocol layer."
      featureItems={FEATURE_ITEMS}
      {...providerProps}
    />
  )
}
