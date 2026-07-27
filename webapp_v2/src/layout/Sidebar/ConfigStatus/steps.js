import { Database, Users, Sparkles, Shield } from 'lucide-react'

// Setup checklist definition (Figma: EVAL Config status, node 14:2).
// `checkKey` maps to a boolean in useConfigStatusStore.checks.
// `to` navigates via router; `action` keys are resolved by the ConfigStatus
// container (flows that need runtime data, e.g. picking a connection).
export const STEP_DEFS = [
  {
    id: 'connect-resource',
    title: 'Connect a resource',
    icon: Database,
    subItems: [
      { checkKey: 'agentDeployed', label: 'Deploy Hoop Agent', to: '/agents/new' },
      { checkKey: 'resourceCreated', label: 'Create a Resource', to: '/resource-catalog' },
      { checkKey: 'sessionRan', label: 'Run your first session', action: 'run-first-session' },
    ],
  },
  {
    id: 'set-up-access',
    title: 'Set up access',
    icon: Users,
    subItems: [
      { checkKey: 'groupsCreated', label: 'Create more groups', to: '/features/access-control' },
      { checkKey: 'peopleAssigned', label: 'Assign people to groups', to: '/features/access-control' },
    ],
  },
  {
    id: 'tune-features',
    title: 'Tune Features',
    icon: Sparkles,
    subItems: [
      { checkKey: 'guardrailsExplored', label: 'Explore Guardrails', to: '/guardrails' },
      { checkKey: 'dataMaskingExplored', label: 'Explore AI Data Masking', to: '/features/data-masking' },
      { checkKey: 'aiAnalyzerEnabled', label: 'Explore AI Session Analyzer', to: '/features/ai-session-analyzer' },
      {
        checkKey: 'protectionLevelSet',
        label: 'Set Protection Level',
        to: '/settings/protection-rules',
        icon: Shield,
        dividerBefore: true,
      },
    ],
  },
]

export function computeProgress(checks) {
  const steps = STEP_DEFS.map((step) => {
    const subItems = step.subItems.map((sub) => ({ ...sub, done: !!checks[sub.checkKey] }))
    return { ...step, subItems, done: subItems.every((sub) => sub.done) }
  })
  const stepsDone = steps.filter((step) => step.done).length
  return {
    steps,
    stepsDone,
    totalSteps: steps.length,
    percent: Math.floor((stepsDone * 100) / steps.length),
    firstIncompleteStepId: steps.find((step) => !step.done)?.id ?? null,
  }
}
