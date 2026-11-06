import {
  decodeForDisplay,
  encodeSecretForSource,
} from '@/pages/Roles/Configure/utils/secretsCodec'

// The secret actions every credential renderer calls. Spread into the
// Configure Role store and into each role store of the resource wizard;
// both keep `connection`, `stagedSecrets`, `fieldSources` and `renames`.
export function credentialsSlice(set) {
  return {
    replaceSecret: (key, base64Value) => {
      set((state) => {
        const isExisting = state.connection?.secret && key in state.connection.secret
        return {
          stagedSecrets: {
            ...state.stagedSecrets,
            [key]: {
              action: isExisting ? 'replace' : 'new',
              value: base64Value,
            },
          },
        }
      })
    },

    // Per-field source (manual / vault-kv1 / vault-kv2 / aws-secrets-manager).
    // Drives the source-selector adornment when the connection method is
    // Secrets Manager. Changing the source for a field already-staged with
    // a new value re-encodes it so the prefix matches.
    setFieldSource: (key, source) => {
      set((state) => {
        const staged = state.stagedSecrets[key]
        const nextSources = { ...state.fieldSources, [key]: source }
        // If the user already typed a new value, re-encode under the new
        // prefix so save() sends the right thing.
        let nextStaged = state.stagedSecrets
        if (staged && staged.value) {
          // staged.value is base64-encoded; decode (stripping any existing
          // provider prefix so we don't double-up) then re-encode for the
          // new source. Empty staged values stay empty.
          const plain = decodeForDisplay(staged.value)
          nextStaged = {
            ...state.stagedSecrets,
            [key]: { ...staged, value: encodeSecretForSource(plain, source) },
          }
        }
        return { fieldSources: nextSources, stagedSecrets: nextStaged }
      })
    },

    deleteSecret: (key) => {
      set((state) => ({
        stagedSecrets: { ...state.stagedSecrets, [key]: { action: 'delete' } },
      }))
    },

    cancelSecretChange: (key) => {
      set((state) => {
        const next = { ...state.stagedSecrets }
        delete next[key]
        // Drop any pending rename for the same key so cancelling a fresh
        // row doesn't leave a phantom rename around.
        const nextRenames = { ...state.renames }
        delete nextRenames[key]
        return { stagedSecrets: next, renames: nextRenames }
      })
    },

    // Rename the Key of a row. We always record the rename in the
    // `renames` map and leave the `stagedSecrets` entry under its
    // original key. That keeps the React `key={envKey}` on the rendered
    // row stable across renames so the row never unmounts mid-typing —
    // moving the entry between keys would lose focus on the next input
    // (Tab from Name to Value) and silently drop the user's typed value.
    // buildSecretsPatch translates the rename into delete-old +
    // replace-new on the wire for both 'new' and persisted entries.
    renameSecret: (originalKey, newKey) => {
      set((state) => {
        if (newKey === originalKey) {
          const renames = { ...state.renames }
          delete renames[originalKey]
          return { renames }
        }
        return { renames: { ...state.renames, [originalKey]: newKey } }
      })
    },
  }
}
