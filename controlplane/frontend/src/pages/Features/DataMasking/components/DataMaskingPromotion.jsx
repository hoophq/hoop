import { Combine, FolderLock, SlidersHorizontal } from 'lucide-react'
import FeaturePromotion from '@/components/FeaturePromotion'
import { docsUrl } from '@/utils/docsUrl'

// Empty-state behavior is gated by the server's DLP `redact_provider`:
//   - mspresidio / alcatraz → "Configure" CTA into the create flow, since
//     both drive masking from data-masking rules.
//   - gcp → docs link + deprecated-provider warning, no create path.
//   - unset → docs link only; there is no provider to call deprecated.
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

const LICENSE_REQUIRED_INFO =
  'Creating masking rules needs an Enterprise license. Add your license, or talk to sales from the dialog.'

// Providers whose masking is driven by data-masking rules, so the org can be
// sent straight into the create flow.
const RULE_DRIVEN_PROVIDERS = ['mspresidio', 'alcatraz']

// `onAddLicense` set means creation is blocked by the license: the primary
// action installs one instead of opening a form whose Save is disabled. The
// provider requirement still comes first, since no license changes it.
export default function DataMaskingPromotion({ redactProvider, onConfigure, onAddLicense }) {
  const createProps = onAddLicense
    ? {
        onPrimaryClick: onAddLicense,
        primaryText: 'Add your license',
        extraInformation: LICENSE_REQUIRED_INFO,
      }
    : {
        onPrimaryClick: onConfigure,
        primaryText: 'Configure Live Data Masking',
      }

  const providerProps = RULE_DRIVEN_PROVIDERS.includes(redactProvider)
    ? createProps
    : {
        docsHref: docsUrl.features.aiDatamasking,
        docsText: 'Go to Live Data Masking Docs',
        extraInformation:
          redactProvider === 'gcp' ? DEPRECATED_GCP_INFO : undefined,
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
