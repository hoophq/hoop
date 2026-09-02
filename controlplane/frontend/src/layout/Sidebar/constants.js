import {
  Container,
  Signature,
  CircleCheckBig,
  MessageSquare,
  Sparkles,
  ShieldCheck,
  VenetianMask,
  Users
} from 'lucide-react';

// The control plane navigation.
//
// Every path here must be claimed by Router.jsx — there is no catch-all, so an entry
// pointing at an unclaimed path sends the user to the 404 page. Gating flags are
// mirrored in features/CommandPalette/constants.js; keep both in sync.

export const MAIN_ITEMS = [
  { label: 'Sidecars', path: '/sidecars', icon: Container, adminOnly: true },
  { label: 'Reviews', path: '/reviews', icon: Signature, adminOnly: true, allowApprover: true }
]

// Slack sits under Reviews because that is where an approval is delivered — a
// decision from the Navigation Routing project, not a filing convenience.
export const REVIEW_ITEMS = [
  {
    label: 'Rules',
    path: '/reviews/rules',
    icon: CircleCheckBig,
    adminOnly: true,
    licenseFeature: 'access-requests'
  },
  { label: 'Slack', path: '/reviews/slack', icon: MessageSquare, adminOnly: true }
]

// Configured here, distributed to the fleet by Feature Configuration Across the Fleet.
// Until that project lands, the configuration a sidecar actually runs still comes from
// its own file.
export const FEATURE_ITEMS = [
  {
    label: 'Session Analyzer',
    path: '/features/ai-session-analyzer',
    icon: Sparkles,
    adminOnly: true,
    licenseFeature: 'ai-session-analyzer'
  },
  {
    label: 'Guardrails',
    path: '/guardrails',
    icon: ShieldCheck,
    adminOnly: true,
    licenseFeature: 'guardrails'
  },
  {
    label: 'Live Data Masking',
    path: '/features/data-masking',
    icon: VenetianMask,
    adminOnly: true,
    licenseFeature: 'data-masking'
  }
]

export const ORGANIZATION_ITEMS = [
  { label: 'Users', path: '/organization/users', icon: Users, adminOnly: true }
]
