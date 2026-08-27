import { ListCheck, ListTodo, Settings2 } from 'lucide-react'
import FeaturePromotion from '@/components/FeaturePromotion'

const FEATURE_ITEMS = [
  {
    icon: <ListTodo size={20} />,
    title: 'Just-in-Time Access Control',
    description:
      'Request temporary access to resources for specific time periods. Automatically manage access when the time expires, reducing security exposure.',
  },
  {
    icon: <ListCheck size={20} />,
    title: 'Multi-Level Approval Workflows',
    description:
      'Configure approval chains with multiple reviewer groups to match your compliance requirements. Commands execute only after all designated approvers grant permission.',
  },
  {
    icon: <Settings2 size={20} />,
    title: 'Integrated Notifications & Audit',
    description:
      'Receive real-time notifications through Slack and other channels when approvals are needed. Maintain complete audit logs for compliance.',
  },
]

// Shown once, before the organization has any rule. Dismissing it is what the
// primary click does — the admin lands on the create form and never sees this
// screen again, even if they abandon the form.
export default function AccessRequestPromotion({ onCreate }) {
  return (
    <FeaturePromotion
      featureName="Access Request"
      mode="empty-state"
      image="access-request-promotion.png"
      description="Streamline secure access with time-based approvals and automated workflows for your critical resources."
      featureItems={FEATURE_ITEMS}
      onPrimaryClick={onCreate}
      primaryText="Create new Access Request rule"
    />
  )
}
