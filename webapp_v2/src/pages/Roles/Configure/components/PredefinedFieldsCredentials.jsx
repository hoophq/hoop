import { Stack } from '@mantine/core'
import SourcedInput from '@/components/SourcedInput'
import SecretField from './SecretField'
import { sourceOptionsFor } from './SecretField/util'
import {
  decodeSecretValue,
  encodeSecretForSource,
  isSecretReference,
  sourceFromEncodedValue,
  SOURCES,
} from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'

// Provider prefixes stripped from the displayed value — the source
// picker conveys which provider applies, so the input itself should
// show the bare reference id (matches CLJS connection_method.cljs).
const PROVIDER_PREFIX_RE = /^(_aws:|_envjson:|_vaultkv1:|_vaultkv2:|_aws_iam_rds:)/

// Renders a list of credential fields driven by a static field schema
// (key, label, required, placeholder, type). Used by every fixed-
// schema connection type — catalog databases, SSH, HTTP proxy, Claude
// Code, Kubernetes token. The caller is responsible for filtering /
// ordering fields appropriately (e.g. SSH's auth-method gating).
//
// Each row picks ONE of two layouts based on what the backend returned
// for the field:
//
//   * **Round-tripped plaintext** — backend returned the actual value.
//     This is the case for every connection shape covered by
//     gateway/api/connections/secrets.go's `shouldRoundTripSecrets`
//     (custom/*, httpproxy/*, application/{ssh,git,github}). Renders
//     a plain SourcedInput pre-filled with the decoded value; saves
//     re-encode under the picked source. Matches CLJS, which never
//     stripped these.
//
//   * **Write-only ("Set" badge)** — backend returned an empty inline
//     value or a `_aws:` / `_vaultkv1:` reference. This is the case
//     for catalog databases where host/user/password stay write-only.
//     Renders the existing `SecretField` flow (Set badge → Replace).
//
// When `availableSources` is supplied (Secrets Manager mode), each row
// also gets a leading source picker. The picked source decides how the
// typed value is encoded on save (manual → bare base64, vault-kv1/2 or
// aws-secrets-manager → prefixed reference).
export default function PredefinedFieldsCredentials({
  connection,
  fields,
  availableSources,
  // When true, every field renders as if it had no existing value —
  // the caller (CredentialsTab) sets this after the user switched the
  // connection method, since pre-existing inline values don't apply
  // to the new method anyway.
  forceNewState,
}) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const fieldSources = useConfigureRoleStore((s) => s.fieldSources)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const setFieldSource = useConfigureRoleStore((s) => s.setFieldSource)

  const currentSecrets = connection.secret || {}
  // When the form forces a fresh-start UX, default each field's source
  // to the first available option (the active provider in Secrets
  // Manager mode) so the adornment shows the right pick on first paint.
  const defaultSource = availableSources?.[0] || SOURCES.MANUAL

  return (
    <Stack gap="lg">
      {fields.map((field) => {
        const envKey = `envvar:${field.key.toUpperCase()}`
        const encodedValue = currentSecrets[envKey]
        const isExisting =
          !forceNewState &&
          envKey in currentSecrets &&
          (encodedValue !== '' || connection.secrets_updated_at != null)
        const isReference = !forceNewState && isSecretReference(encodedValue)
        const referenceText = isReference ? decodeSecretValue(encodedValue) : ''
        const staged = stagedSecrets[envKey]
        // Source priority: explicit pick → detection from the encoded
        // value (so an existing reference picks up its provider) →
        // default for the current mode.
        const encodedForDetection = staged ? staged.value : encodedValue || ''
        const source =
          fieldSources[envKey] ||
          (encodedForDetection ? sourceFromEncodedValue(encodedForDetection) : null) ||
          defaultSource

        // Plaintext round-trip: backend returned an inline value (not
        // stripped, not a reference). Render an editable field with the
        // value visible. Staged changes win — once the user has typed,
        // we show their staged value via SecretField's editing state.
        const isRoundTrippedPlain =
          !forceNewState && encodedValue && !isReference && !staged
        if (isRoundTrippedPlain) {
          const decodedValue = decodeSecretValue(encodedValue).replace(
            PROVIDER_PREFIX_RE,
            '',
          )
          return (
            <SourcedInput
              key={envKey}
              label={field.label}
              required={field.required}
              placeholder={field.placeholder}
              type={field.type}
              value={decodedValue}
              onChange={(plain) =>
                replaceSecret(envKey, encodeSecretForSource(plain, source))
              }
              source={source}
              sources={sourceOptionsFor(availableSources)}
              onSourceChange={(nextSource) => {
                // Stage the current value under the new source before
                // updating fieldSources so save() always sends the
                // right encoding — even when the user just toggles the
                // picker without typing.
                replaceSecret(
                  envKey,
                  encodeSecretForSource(decodedValue, nextSource),
                )
                setFieldSource(envKey, nextSource)
              }}
            />
          )
        }

        return (
          <SecretField
            key={envKey}
            label={field.label}
            required={field.required}
            placeholder={field.placeholder}
            type={field.type}
            isExisting={isExisting}
            isReference={isReference}
            referenceText={referenceText}
            allowDelete={false}
            stagedAction={staged?.action}
            stagedValue={staged?.value ? decodeSecretValue(staged.value).replace(
              PROVIDER_PREFIX_RE,
              '',
            ) : ''}
            secretsUpdatedAt={connection.secrets_updated_at}
            source={source}
            availableSources={availableSources}
            onSourceChange={(s) => setFieldSource(envKey, s)}
            onReplace={(plain) =>
              replaceSecret(envKey, encodeSecretForSource(plain, source))
            }
            onChangeStaged={(plain) =>
              replaceSecret(envKey, encodeSecretForSource(plain, source))
            }
            onCancel={() => cancelSecretChange(envKey)}
          />
        )
      })}
    </Stack>
  )
}
