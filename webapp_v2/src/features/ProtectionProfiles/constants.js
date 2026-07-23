import { Award, Shield, ShieldCheck, SlidersHorizontal } from 'lucide-react'

// Sentinel used by the UI for the "Manual configuration" card. The API models
// manual configuration as profile = null — translate at the service boundary.
export const MANUAL_PROFILE = 'manual'

export function toApiProfile(value) {
  return value === MANUAL_PROFILE ? null : value
}

export function fromApiProfile(profile) {
  return profile === null || profile === undefined ? MANUAL_PROFILE : profile
}

// Display catalog for the five protection profiles served by the gateway
// (gateway/services/protection_profiles_catalog.go). Copy mirrors the Figma
// design — profiles generate evidence for HIPAA/SOC 2, they do not by
// themselves make an organization compliant, so keep that copy discipline.
export const COMPLIANCE_PROFILES = [
  {
    id: 'hipaa-ready',
    title: 'HIPAA Ready',
    enterprise: true,
    icon: Award,
    iconColor: 'indigo',
    iconVariant: 'filled',
    bullets: [
      'Strict PHI and PII masking',
      'PHI queries and bulk exports blocked',
      'Destructive SQL and shell commands blocked',
      'JIT access, 4h max, approval required',
      'Command approval on every execution',
      'High-risk sessions blocked by AI',
      'SSN output redaction',
    ],
  },
  {
    id: 'soc2-type2',
    title: 'SOC2 Type II',
    enterprise: true,
    icon: Award,
    iconColor: 'indigo',
    iconVariant: 'filled',
    bullets: [
      'PII, financial and credential masking',
      'Destructive SQL and exports blocked',
      'JIT access, 8h max, approval required',
      'Command approval on production changes',
      'High-risk sessions routed to approval',
      'Audit-ready session reports',
    ],
  },
]

export const GENERAL_PROFILES = [
  {
    id: 'protection-permissive',
    title: 'Essential guardrails',
    enterprise: false,
    badge: { label: 'Free', color: 'green' },
    icon: Shield,
    iconColor: 'indigo',
    iconVariant: 'light',
    description:
      'Guards basic mistakes: DELETE or UPDATE operations without the WHERE clause, and masks API Keys.',
  },
  {
    id: 'protection-medium',
    title: 'Balanced',
    enterprise: true,
    badge: { label: 'Enterprise', color: 'indigo' },
    icon: Shield,
    iconColor: 'gray',
    iconVariant: 'light',
    description:
      'The production default. Personal data masked, destructive operations blocked, access expires after 8 hours.',
  },
  {
    id: 'protection-high',
    title: 'Maximum',
    enterprise: true,
    badge: { label: 'Enterprise', color: 'indigo' },
    icon: ShieldCheck,
    iconColor: 'green',
    iconVariant: 'light',
    description:
      'For crown-jewel systems. Everything masked, every execution approved, access expires in 2 hours.',
  },
]

export const MANUAL_CARD = {
  id: MANUAL_PROFILE,
  title: 'Manual configuration',
  enterprise: false,
  icon: SlidersHorizontal,
  iconColor: 'gray',
  iconVariant: 'light',
  description: 'Skip the defaults. Build your own masking, guardrails and access rules.',
}
