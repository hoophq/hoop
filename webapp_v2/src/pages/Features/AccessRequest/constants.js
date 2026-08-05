export const LIST_PATH = '/features/access-request'
export const NEW_PATH = '/features/access-request/new'
export const EDIT_PATH = '/features/access-request/edit'

// Still a ClojureScript page (B1.2 / EVL-139) — React Router hands the path to
// the CLJS catch-all, so the upgrade flow keeps working until it is migrated.
export const UPGRADE_PLAN_PATH = '/upgrade-plan'

export const PROMOTION_SEEN_STORAGE_KEY = 'access-request-promotion-seen'

export const ACCESS_TYPE = {
  JIT: 'jit',
  COMMAND: 'command',
}

// How long a just-in-time access request stays valid, in seconds. Values are
// strings because that is what Mantine's Select carries; the payload parses
// them back to integers.
export const TIME_RANGE_OPTIONS = [
  { value: '900', label: '15 minutes' },
  { value: '1800', label: '30 minutes' },
  { value: '3600', label: '1 hour' },
  { value: '7200', label: '2 hours' },
  { value: '14400', label: '4 hours' },
  { value: '28800', label: '8 hours' },
  { value: '57600', label: '16 hours' },
  { value: '86400', label: '24 hours' },
  { value: '115200', label: '32 hours' },
  { value: '144000', label: '40 hours' },
  { value: '172800', label: '48 hours' },
]

export const FREE_LICENSE_MESSAGE =
  'Enable creating unlimited rules and applying to multiple resource roles for Command type requests by upgrading your plan.'

export const MANAGED_RULE_MESSAGE =
  'This rule is managed by Hoop as part of your protection profile. Only approval settings and group lists can be changed.'
