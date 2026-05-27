import { SOURCE_LABELS } from '../../utils/secretsCodec'

export function sourceOptionsFor(availableSources) {
  return (availableSources || []).map((s) => ({ value: s, label: SOURCE_LABELS[s] || s }))
}

export function formatTimestamp(iso) {
  if (!iso) return null
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return null
  }
}
