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
import { ROLE_ADMIN, ROLE_APPROVER } from '@/utils/roles'

// Navigation targets for the palette. Gating flags (role / selfhostedOnly /
// featureFlag / licenseFeature) mirror layout/Sidebar/constants.js and are applied with
// the same shouldHide() helper — keep both in sync when a page's gating changes.
//
// Every entry must resolve to a route in Router.jsx. The palette navigates, and the
// control plane has no catch-all: an entry pointing at a path the router does not claim
// sends the user to the 404 page.

export const SUGGESTION_ITEMS = [
  { id: 'sidecars', label: 'Sidecars', description: 'Connected sidecars and their resources', icon: Container, path: '/sidecars', role: ROLE_ADMIN },
  { id: 'reviews', label: 'Reviews', description: 'Pending approvals', icon: Signature, path: '/reviews', role: ROLE_APPROVER },
]

export const QUICK_ACCESS_ITEMS = [
  { id: 'review-rules', label: 'Review Rules', description: 'Approval rules referenced by sidecars', icon: CircleCheckBig, path: '/reviews/rules', role: ROLE_ADMIN, licenseFeature: 'access-requests' },
  { id: 'review-slack', label: 'Slack', description: 'Where approvals are delivered', icon: MessageSquare, path: '/reviews/slack', role: ROLE_ADMIN },
  { id: 'ai-session-analyzer', label: 'Session Analyzer', description: 'Risk classification rules', icon: Sparkles, path: '/features/ai-session-analyzer', role: ROLE_ADMIN, licenseFeature: 'ai-session-analyzer' },
  { id: 'guardrails', label: 'Guardrails', description: 'Configure guardrails', icon: ShieldCheck, path: '/guardrails', role: ROLE_ADMIN, licenseFeature: 'guardrails' },
  { id: 'data-masking', label: 'Live Data Masking', description: 'Configure live data masking', icon: VenetianMask, path: '/features/data-masking', role: ROLE_ADMIN, licenseFeature: 'data-masking' },
  { id: 'users', label: 'Users', description: 'Invite and manage administrators and approvers', icon: Users, path: '/organization/users', role: ROLE_ADMIN },
]
