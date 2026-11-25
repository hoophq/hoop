import { SearchCode, ShieldCheck, Sparkles } from 'lucide-react'
import FeaturePromotion from '@/components/FeaturePromotion'

const FEATURE_ITEMS = [
  {
    icon: <SearchCode size={20} />,
    title: 'Real-Time Risk Analysis',
    description:
      'Analyze commands before execution to prevent security and reliability risks.',
  },
  {
    icon: <ShieldCheck size={20} />,
    title: 'Configurable Rules',
    description: 'Admins define alert or block rules per Resource.',
  },
  {
    icon: <Sparkles size={20} />,
    title: 'Context-Aware AI Decisions',
    description:
      'Use schema, indexes, and resource context to deliver accurate, trustworthy risk assessments.',
  },
]

export default function AiSessionAnalyzerPromotion({ onConfigure }) {
  return (
    <FeaturePromotion
      featureName="AI Analyzer"
      mode="empty-state"
      image="ai-session-analyzer-promotion.png"
      description="Score session risk with AI and block or route risky commands."
      featureItems={FEATURE_ITEMS}
      onPrimaryClick={onConfigure}
      primaryText="Configure AI Analyzer"
    />
  )
}
