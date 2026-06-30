import { useState } from 'react'
import ReadOnlyStatus from './ReadOnlyStatus'
import ReplacingInput from './ReplacingInput'
import NewInput from './NewInput'

// SecretField — write-only credential editor.
//
// Renders one of three mutually-exclusive states based on the props:
//
//   * "set"      — an existing inline secret. Value is never shown.
//                  Offers Replace; also acts as "reference" when the
//                  underlying value points at a provider (Vault/AWS).
//   * "editing"  — user clicked Replace or is staging a new value.
//                  Cancel removes the staged change.
//   * "new"      — there is no existing value (key absent in connection);
//                  a plain editable input. Used for custom-type adds.
//
// stagedAction === 'replace' | 'delete' | 'new' takes priority over the
// underlying connection state.
export default function SecretField({
  label,
  description,
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
        description={description}
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
        description={description}
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
      description={description}
      required={required}
      isReference={isReference}
      referenceText={referenceText}
      onReplace={() => {
        setEditing(true)
        // Start staged with empty input so HTML5 required validation works.
        onReplace('')
      }}
    />
  )
}
