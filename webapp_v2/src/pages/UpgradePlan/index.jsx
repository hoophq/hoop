import { useNavigate } from 'react-router-dom'
import { Stack } from '@mantine/core'
import { ArrowLeft, ListChecks, MessagesSquare, Sparkles } from 'lucide-react'
import Button from '@/components/Button'
import FeaturePromotion from '@/components/FeaturePromotion'
import { useUserStore } from '@/stores/useUserStore'

const INTERCOM_MESSAGE = 'I want to upgrade my current plan'
const SALES_URL = 'https://hoop.dev/meet'

const FEATURES = [
  {
    icon: <Sparkles size={20} />,
    title: 'AI-enhanced developer experience',
    description:
      'Power up development with AI-driven query suggestions and automated data masking while maintaining security standards.',
  },
  {
    icon: <ListChecks size={20} />,
    title: 'Complete visibility and control',
    description:
      'Monitor database and infrastructure interaction with detailed Session recordings and instant alerts in your favorite tools.',
  },
  {
    icon: <MessagesSquare size={20} />,
    title: 'Enterprise-grade support',
    description:
      'Access priority support through Slack, Teams, or email, plus dedicated onboarding to accelerate your team experience.',
  },
]

// /upgrade-plan: a free-license limit sends the admin here (Access Request).
export default function UpgradePlan() {
  const navigate = useNavigate()
  const showIntercomMessage = useUserStore((s) => s.showIntercomMessage)

  const requestDemo = () => {
    if (showIntercomMessage(INTERCOM_MESSAGE)) return
    window.open(SALES_URL, '_blank', 'noopener,noreferrer')
  }

  return (
    <Stack gap="xl">
      <Button
        variant="transparent"
        color="gray"
        leftSection={<ArrowLeft size={16} />}
        onClick={() => navigate(-1)}
        px={0}
        w="fit-content"
      >
        Back
      </Button>
      <FeaturePromotion
        featureName="Enterprise"
        title="Get more for your Resources"
        mode="upgrade-plan"
        imageSrc="/images/upgrade-plan.png"
        description="Upgrade to the Enterprise plan and boost your experience."
        featureItems={FEATURES}
        primaryText="Request a demo"
        onPrimaryClick={requestDemo}
      />
    </Stack>
  )
}
