// Converts a credential entry from connections-metadata.json into the
// shape the React credential renderers expect. Mirrors CLJS
// metadata_driven.cljs:metadata-credential->form-field — every credential
// defaults to `password` so the input gets the masked / eye-toggle UX,
// unless the metadata explicitly marks it as a filesystem field
// (rendered as a textarea).
export function jsonCredentialToField(entry) {
  const { name, type, required, description, placeholder } = entry
  return {
    key: name.toLowerCase(),
    envVar: name,
    label: name.split('_').join(' '),
    required: Boolean(required),
    placeholder: placeholder || description || undefined,
    description: description || undefined,
    type: type === 'filesystem' ? 'textarea' : 'password',
  }
}
