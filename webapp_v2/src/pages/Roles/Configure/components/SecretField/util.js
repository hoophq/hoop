import { SOURCE_LABELS } from '@/pages/Roles/Configure/utils/secretsCodec'

export function sourceOptionsFor(availableSources) {
  return (availableSources || []).map((s) => ({ value: s, label: SOURCE_LABELS[s] || s }))
}

// Fixed-width dotted mask shown in the "set" state. The real value is never
// returned by the API, so the length is purely cosmetic.
export const SECRET_MASK = '•'.repeat(24)

export function formatTimestamp(iso) {
  if (!iso) return null
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return null
  }
}
