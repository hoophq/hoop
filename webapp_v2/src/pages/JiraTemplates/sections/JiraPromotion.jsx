import { FileLock2, ListCheck, Settings2 } from 'lucide-react'
import FeaturePromotion from '@/components/FeaturePromotion'

// Shown while the org has no Jira integration configured; the primary action
// switches to the Configuration tab instead of navigating away.
export default function JiraPromotion({ onConfigure }) {
  return (
    <FeaturePromotion
      featureName="JIRA Templates"
      image="jira-pomotion.png"
      description="Automate change management and security workflows."
      featureItems={[
        {
          icon: <ListCheck size={20} />,
          title: 'Automated Change Management',
          description:
            'Reduce manual documentation and administrative overhead by automatically creating and tracking Jira tickets for every infrastructure access request.',
        },
        {
          icon: <Settings2 size={20} />,
          title: 'Seamless Workflow Integration',
          description:
            'Link access requests directly to Jira projects and request types with contextual information.',
        },
        {
          icon: <FileLock2 size={20} />,
          title: 'Flexible User Prompts & Data Collection',
          description:
            'Request additional information from users during access workflows. Map manual or automated data to Jira fields.',
        },
      ]}
      onPrimaryClick={onConfigure}
      primaryText="Configure Jira Integration"
    />
  )
}
