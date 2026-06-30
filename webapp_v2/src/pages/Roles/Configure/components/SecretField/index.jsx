import { useState } from 'react'
import ReadOnlyStatus from './ReadOnlyStatus'
import ReplacingInput from './ReplacingInput'
import NewInput from './NewInput'

// SecretField — write-only credential editor.
//
// Renders one of three mutually-exclusive states:
//   * "set"      — an existing inline secret. The value is never shown; a
//                  masked input offers Replace. References render their
//                  pointer text verbatim.
//   * "editing"  — the user clicked Replace or is staging a new value.
//                  Restore drops the staged change.
//   * "new"      — no existing value (key absent); a plain editable input.
//
// stagedAction === 'replace' | 'new' takes priority over the underlying
// connection state.
export default function SecretField({
  label,
  required = false,
  placeholder,
  type,
  isExisting,
  isReference,
  referenceText,
  stagedAction,
  stagedValue = '',
  source,
  availableSources,
  onSourceChange,
  onReplace,
  onChangeStaged,
  onCancel,
  onRemove,
}) {
  const [editing, setEditing] = useState(false)

  const state = (() => {
    if (stagedAction === 'replace' || stagedAction === 'new' || editing) return 'editing'
    if (!isExisting) return 'new'
    return 'set'
  })()

  if (state === 'editing') {
    return (
      <ReplacingInput
        label={label}
        required={required}
        placeholder={placeholder}
        type={type}
        value={stagedValue}
        onChange={(plain) => {
          if (!stagedAction) onReplace(plain)
          else onChangeStaged(plain)
        }}
        onCancel={() => {
          setEditing(false)
          onCancel()
        }}
        source={source}
        availableSources={availableSources}
        onSourceChange={onSourceChange}
      />
    )
  }

  if (state === 'new') {
    return (
      <NewInput
        label={label}
        required={required}
        placeholder={placeholder}
        type={type}
        value={stagedValue}
        onChange={(plain) => onReplace(plain)}
        onRemove={onRemove}
        source={source}
        availableSources={availableSources}
        onSourceChange={onSourceChange}
      />
    )
  }

  return (
    <ReadOnlyStatus
      label={label}
      required={required}
      type={type}
      isReference={isReference}
      referenceText={referenceText}
      onReplace={() => {
        setEditing(true)
        // Start staged empty so HTML5 required validation works.
        onReplace('')
      }}
    />
  )
}
