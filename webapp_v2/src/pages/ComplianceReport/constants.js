import {
  CheckCircle2,
  AlertTriangle,
  XCircle,
  MinusCircle,
  HelpCircle,
  Link2,
  Info,
  UserRoundCheck,
  LockKeyhole,
  ShieldCheck,
  ListChecks,
  Activity,
  ServerCog,
} from 'lucide-react'

// Status → icon/color/label, mirroring the spec's item status variations.
// Colors follow the app's semantic badge convention (green/yellow/red/gray,
// indigo for informational/delegated).
export const STATUS_META = {
  compliant: { icon: CheckCircle2, color: 'green', label: 'Compliant' },
  warning: { icon: AlertTriangle, color: 'yellow', label: 'Partially met' },
  non_compliant: { icon: XCircle, color: 'red', label: 'Not implemented' },
  not_applicable: { icon: MinusCircle, color: 'gray', label: 'Not applicable' },
  unable_to_verify: { icon: HelpCircle, color: 'gray', label: 'Unable to verify' },
  idp_dependent: { icon: Link2, color: 'indigo', label: 'Delegated to IdP' },
  informational: { icon: Info, color: 'indigo', label: 'Informational' },
}

// Overall score level colors (0-1000: low 0-499, moderate 500-749,
// strong 750-1000); shared with the per-framework score bar.
export const LEVEL_META = {
  low: { color: 'red' },
  moderate: { color: 'yellow' },
  strong: { color: 'green' },
}

export const CATEGORY_SUBTITLES = {
  identity: 'User identification & authentication',
  access_control: 'Access and privilege management',
  data_protection: 'Sensitive data masking & encryption',
  audit_trail: 'Activity logging & evidence capture',
  monitoring_response: 'Detection & event forwarding',
  infrastructure: 'Platform health & connectivity',
}

// One color and icon per category, used by the summary cards and the action
// tags (design reference palette).
export const CATEGORY_COLORS = {
  identity: 'red',
  access_control: 'blue',
  data_protection: 'yellow',
  audit_trail: 'pink',
  monitoring_response: 'violet',
  infrastructure: 'green',
}

export const CATEGORY_ICONS = {
  identity: UserRoundCheck,
  access_control: LockKeyhole,
  data_protection: ShieldCheck,
  audit_trail: ListChecks,
  monitoring_response: Activity,
  infrastructure: ServerCog,
}

// Display order of the summary cards (design reference, row-major in the
// 3-column grid). Unknown ids sort last in API order.
export const CATEGORY_ORDER = [
  'identity',
  'audit_trail',
  'monitoring_response',
  'access_control',
  'data_protection',
  'infrastructure',
]

// Catalog docs targets are gateway-relative ("/docs/…"); the public docs site
// hosts them. Named distinctly from the @/utils/docsUrl route map, which
// serves fixed feature links.
export const catalogDocsUrl = (target) => `https://hoop.dev${target}`
