import {
  Container,
  Signature,
  CircleCheckBig,
  MessageSquare,
  Sparkles,
  ShieldCheck,
  VenetianMask,
  Users,
} from 'lucide-react'

// Navigation targets for the palette. Gating flags (adminOnly / selfhostedOnly /
// featureFlag / licenseFeature) mirror layout/Sidebar/constants.js and are applied with
// the same shouldHide() helper — keep both in sync when a page's gating changes.
//
// Every entry must resolve to a route in Router.jsx. The palette navigates, and the
// control plane has no catch-all: an entry pointing at a path the router does not claim
// sends the user to the 404 page.

export const SUGGESTION_ITEMS = [
  { id: 'sidecars', label: 'Sidecars', description: 'Connected sidecars and their resources', icon: Container, path: '/sidecars' },
  { id: 'reviews', label: 'Reviews', description: 'Pending approvals', icon: Signature, path: '/reviews' },
]

export const QUICK_ACCESS_ITEMS = [
  { id: 'review-rules', label: 'Review Rules', description: 'Approval rules referenced by sidecars', icon: CircleCheckBig, path: '/reviews/rules', adminOnly: true, licenseFeature: 'access-requests' },
  { id: 'review-slack', label: 'Slack', description: 'Where approvals are delivered', icon: MessageSquare, path: '/reviews/slack', adminOnly: true },
  { id: 'ai-session-analyzer', label: 'Session Analyzer', description: 'Risk classification rules', icon: Sparkles, path: '/features/ai-session-analyzer', adminOnly: true, licenseFeature: 'ai-session-analyzer' },
  { id: 'guardrails', label: 'Guardrails', description: 'Configure guardrails', icon: ShieldCheck, path: '/guardrails', adminOnly: true, licenseFeature: 'guardrails' },
  { id: 'data-masking', label: 'Live Data Masking', description: 'Configure live data masking', icon: VenetianMask, path: '/features/data-masking', adminOnly: true, licenseFeature: 'data-masking' },
  { id: 'users', label: 'Administrators', description: 'Invite and manage administrators', icon: Users, path: '/organization/users', adminOnly: true },
]
