// Converts a credential entry from connections-metadata.json into the
// shape the React credential renderers expect.
//
// JSON entry (from hoophq/documentation:store/connections.json):
//   { name: "HOST", type: "env-var", required: true,
//     description: "...", placeholder: "localhost" }
//
// Mirrors the CLJS mapper at
// webapp/src/webapp/connections/views/setup/metadata_driven.cljs:45-59.
//
// Notes on field naming:
// - `key` is the lower-cased name. React state keys for predefined
//   credentials live as lowercase (e.g. drafts.host); the existing
//   PredefinedFieldsCredentials helper uppercases back to envvar form
//   when persisting.
// - `label` mirrors the CLJS render: split on `_`, rejoin with a space,
//   case preserved. So "HOST" stays "HOST"; "AWS_ACCESS_KEY_ID" stays
//   "AWS ACCESS KEY ID". This keeps the UI honest about which env-var
//   the user is editing and avoids hand-tuning a second time.
export function jsonCredentialToField(entry) {
  const { name, type, required, description, placeholder } = entry
  return {
    key: name.toLowerCase(),
    envVar: name,
    label: name.split('_').join(' '),
    required: Boolean(required),
    placeholder: placeholder || description || undefined,
    type: type === 'filesystem' ? 'textarea' : undefined,
  }
}
