import {
  canAccessNativeClient,
  canHoopCli,
  canOpenWebTerminal,
} from '@/utils/connectionPolicy'
import { ACCESS_TYPE } from './constants'

// Rule names are resource identifiers on the gateway (ValidateResourceName),
// so spaces collapse into underscores and anything outside the allowed set is
// dropped as the admin types.
export function sanitizeRuleName(value) {
  return String(value ?? '')
    .replace(/\s+/g, '_')
    .replace(/[^A-Za-z0-9_.-]/g, '')
}

export function accessTypeLabel(accessType) {
  switch (accessType) {
    case ACCESS_TYPE.JIT:
      return 'Just-in-Time'
    case ACCESS_TYPE.COMMAND:
      return 'by Command'
    case ACCESS_TYPE.JIT_COMMAND:
      return 'Just-in-Time + Command'
    default:
      return accessType
  }
}

// Which resource roles a rule of this type can actually govern. "command"
// requests run in the browser terminal; just-in-time requests open the
// resource through a native client or the CLI, and the union of those two
// paths is every role that allows connecting.
export function isEligibleForAccessType(accessType, connection) {
  if (accessType === ACCESS_TYPE.COMMAND) return canOpenWebTerminal(connection)
  if (accessType === ACCESS_TYPE.JIT) {
    return canAccessNativeClient(connection) || canHoopCli(connection)
  }
  if (accessType === ACCESS_TYPE.JIT_COMMAND) {
    return (
      isEligibleForAccessType(ACCESS_TYPE.COMMAND, connection) ||
      isEligibleForAccessType(ACCESS_TYPE.JIT, connection)
    )
  }
  return false
}

// Whether the given access type covers this half ("jit" | "command").
export function accessTypeIncludes(accessType, half) {
  return accessType === half || accessType === ACCESS_TYPE.JIT_COMMAND
}

// Next access type when the card for `half` is toggled. Returns null when the
// click would deselect the only selected type — a rule always keeps at least one.
export function toggledAccessType(current, half) {
  if (current === half) return null
  if (current === ACCESS_TYPE.JIT_COMMAND) {
    return half === ACCESS_TYPE.JIT ? ACCESS_TYPE.COMMAND : ACCESS_TYPE.JIT
  }
  return ACCESS_TYPE.JIT_COMMAND
}

// The attribute owns the association, so its `access_request_rule_names` is
// the authoritative side. Rules carry a copy in `attributes`, which is the
// fallback for an attribute the list request didn't return.
export function rulesForAttribute(rules, attributes, attributeName) {
  const attribute = attributes.find((a) => a.name === attributeName)
  const ruleNames = attribute?.access_request_rule_names ?? []
  if (ruleNames.length > 0) {
    const wanted = new Set(ruleNames)
    return rules.filter((rule) => wanted.has(rule.name))
  }
  return rules.filter((rule) => (rule.attributes ?? []).includes(attributeName))
}

export function filterRules(rules, { attributes, roleName, attributeName }) {
  let filtered = rules
  if (roleName) {
    filtered = filtered.filter((rule) =>
      (rule.connection_names ?? []).includes(roleName),
    )
  }
  if (attributeName) {
    filtered = rulesForAttribute(filtered, attributes, attributeName)
  }
  return [...filtered].sort((a, b) => a.name.localeCompare(b.name))
}
