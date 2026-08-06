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
    description: 'Admins define alert or block policies per resource.',
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
      featureName="AI Session Analyzer"
      mode="empty-state"
      image="ai-session-analyzer-promotion.png"
      description="Monitor terminal sessions and resource usage in real time."
      featureItems={FEATURE_ITEMS}
      onPrimaryClick={onConfigure}
      primaryText="Configure AI Session Analyzer"
    />
  )
}
