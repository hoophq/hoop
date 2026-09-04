import { Database, Users, Sparkles, ShieldCogCorner, ShieldCheck } from 'lucide-react'

// Setup checklist definition (Figma: EVAL Config status, node 14:2).
// `checkKey` mirrors the `checks` object from GET /orgs/onboarding — hence
// snake_case. `to` navigates; `action` is resolved by the ConfigStatus container.
export const STEP_DEFS = [
  {
    id: 'connect-resource',
    title: 'Connect a resource',
    icon: Database,
    subItems: [
      { checkKey: 'agent_deployed', label: 'Deploy Hoop Agent', to: '/agents/new' },
      { checkKey: 'resource_created', label: 'Create a Resource', to: '/resource-catalog' },
      { checkKey: 'session_ran', label: 'Run your first session', action: 'run-first-session' },
    ],
  },
  {
    id: 'set-up-access',
    title: 'Set up access',
    icon: Users,
    subItems: [
      { checkKey: 'groups_created', label: 'Create more groups', to: '/features/access-control' },
      { checkKey: 'people_assigned', label: 'Assign people to groups', to: '/organization/users' },
    ],
  },
  {
    id: 'tune-features',
    title: 'Tune Features',
    icon: Sparkles,
    subItems: [
      { checkKey: 'guardrails_explored', label: 'Explore Guardrails', to: '/guardrails' },
      { checkKey: 'data_masking_explored', label: 'Explore AI Data Masking', to: '/features/data-masking' },
      { checkKey: 'ai_analyzer_enabled', label: 'Explore AI Session Analyzer', to: '/features/ai-session-analyzer' },
      {
        checkKey: 'protection_level_set',
        label: 'Set Protection Level',
        to: '/settings/protection-rules',
        icon: ShieldCogCorner,
        doneIcon: ShieldCheck,
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
    // How long a dismiss lasts depends on this — see useUIStore.
    subItemsLeft: steps.reduce((n, step) => n + step.subItems.filter((sub) => !sub.done).length, 0),
  }
}
