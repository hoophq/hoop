import { useEffect } from 'react'
import { Stack } from '@mantine/core'
import SourcedInput from '@/components/SourcedInput'
import SecretField from '../../../components/SecretField'
import { sourceOptionsFor } from '../../../components/SecretField/util'
import {
  decodeForDisplay,
  decodeSecretValue,
  encodeSecretForSource,
  isSecretReference,
  sourceFromEncodedValue,
  SOURCES,
} from '../../../utils/secretsCodec'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'
import { useConfigureRoleStore } from '../../../store'

const AWS_IAM_PASS_VALUE = 'authtoken'
const AWS_IAM_PASS_ENCODED = encodeSecretForSource(
  AWS_IAM_PASS_VALUE,
  SOURCES.AWS_IAM_ROLE,
)

// Renders a list of credential fields driven by a static field schema
// (key, label, required, placeholder, type, description). Used by every
// fixed-schema renderer — CatalogRenderer (catalog schema), SshRenderer
// (SSH auth fields), and the inline-schema forms (ClaudeCodeRenderer,
// HttpProxyRenderer). The caller is responsible for filtering /
// ordering fields appropriately (e.g. SSH's auth-method gating).
//
// Each row picks ONE of two layouts based on what the backend returned
// for the field:
//
//   * **Round-tripped plaintext** — backend returned the actual value.
//     This is the case for the bespoke renderers covered by
//     gateway/api/connections/secrets.go's `shouldRoundTripSecrets`
//     (application/ssh, httpproxy/*, custom/(empty|linux-vm|kubernetes-token)).
//     Renders a plain SourcedInput pre-filled with the decoded value;
//     saves re-encode under the picked source. Matches CLJS, which
//     never stripped these.
//
//   * **Write-only ("Set" badge)** — backend returned an empty inline
//     value or a `_aws:` / `_vaultkv1:` reference. This is the case
//     for catalog applications, databases, and catalog custom subtypes.
//     Renders the existing `SecretField` flow (Set badge → Replace).
//
// When `availableSources` is supplied (Secrets Manager mode), each row
// also gets a leading source picker. The picked source decides how the
// typed value is encoded on save (manual → bare base64, vault-kv1/2 or
// aws-secrets-manager → prefixed reference).
//
// When `connectionMethod` is AWS IAM Role (postgres/mysql only), the
// PASS field is hidden and force-staged to `_aws_iam_rds:authtoken`;
// USER is encoded with the `_aws_iam_rds:` prefix. Mirrors CLJS
// process_form.cljs:113-138.
export default function PredefinedFields({
  connection,
  fields,
  availableSources,
  // When true, every field renders as if it had no existing value —
  // the caller (CredentialsTab) sets this after the user switched the
  // connection method, since pre-existing inline values don't apply
  // to the new method anyway.
  forceNewState,
  connectionMethod,
}) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const fieldSources = useConfigureRoleStore((s) => s.fieldSources)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const setFieldSource = useConfigureRoleStore((s) => s.setFieldSource)

  const isAwsIam = connectionMethod === CONNECTION_METHODS.AWS_IAM
  const currentSecrets = connection.secret || {}
  // When the form forces a fresh-start UX, default each field's source
  // to the first available option (the active provider in Secrets
  // Manager mode) so the adornment shows the right pick on first paint.
  const defaultSource = availableSources?.[0] || SOURCES.MANUAL

  // AWS IAM mode forces PASS=authtoken (encoded with the _aws_iam_rds:
  // prefix). The field is hidden from the UI; we stage the value here
  // so save() picks it up. Skipped when the connection already carries
  // the right value (existing AWS IAM connection on initial load).
  const passField = fields.find((f) => f.key.toLowerCase() === 'pass')
  const passEnvKey = passField ? `envvar:${passField.key.toUpperCase()}` : null
  const persistedPass = connection.secret?.[passEnvKey]
  const stagedPass = stagedSecrets[passEnvKey]?.value
  useEffect(() => {
    if (!isAwsIam || !passEnvKey) return
    if (stagedPass === AWS_IAM_PASS_ENCODED) return
    if (persistedPass === AWS_IAM_PASS_ENCODED && !forceNewState && !stagedPass) return
    replaceSecret(passEnvKey, AWS_IAM_PASS_ENCODED)
  }, [isAwsIam, passEnvKey, forceNewState, persistedPass, stagedPass, replaceSecret])

  // Hide PASS in AWS IAM mode — its value is form-driven, not user-driven.
  const visibleFields = isAwsIam
    ? fields.filter((f) => f.key.toLowerCase() !== 'pass')
    : fields

  return (
    <Stack gap="lg">
      {visibleFields.map((field) => {
        const envKey = `envvar:${field.key.toUpperCase()}`
        const encodedValue = currentSecrets[envKey]
        // Key presence is the authoritative existence signal. The gateway
        // strips inline values for write-only shapes (encodedValue becomes
        // '') but preserves the key set, so a present key means the
        // credential exists on the server. Don't gate on
        // `secrets_updated_at` — legacy rows from before the migration
        // have it NULL.
        const isExisting = !forceNewState && envKey in currentSecrets
        const isReference = !forceNewState && isSecretReference(encodedValue)
        const referenceText = isReference ? decodeSecretValue(encodedValue) : ''
        const staged = stagedSecrets[envKey]
        // Source priority: explicit pick → detection from the encoded
        // value (so an existing reference picks up its provider) →
        // default for the current mode.
        const encodedForDetection = staged ? staged.value : encodedValue || ''
        const detectedSource =
          fieldSources[envKey] ||
          (encodedForDetection ? sourceFromEncodedValue(encodedForDetection) : null) ||
          defaultSource
        // AWS IAM Role mode overrides the per-field source for USER (and
        // PASS, though PASS is hidden above). All other fields keep the
        // user-picked or detected source.
        const fieldKeyLower = field.key.toLowerCase()
        const isAwsIamScoped = isAwsIam && (fieldKeyLower === 'user' || fieldKeyLower === 'pass')
        const source = isAwsIamScoped ? SOURCES.AWS_IAM_ROLE : detectedSource

        // Plaintext round-trip: backend returned an inline value (not
        // stripped, not a reference). Render an editable field with the
        // value visible. Staged changes win — once the user has typed,
        // we show their staged value via SecretField's editing state.
        const isRoundTrippedPlain =
          !forceNewState && encodedValue && !isReference && !staged
        if (isRoundTrippedPlain) {
          const decodedValue = decodeForDisplay(encodedValue)
          return (
            <SourcedInput
              key={envKey}
              label={field.label}
              description={field.description}
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
            description={field.description}
            required={field.required}
            placeholder={field.placeholder}
            type={field.type}
            isExisting={isExisting}
            isReference={isReference}
            referenceText={referenceText}
            allowDelete={false}
            stagedAction={staged?.action}
            stagedValue={decodeForDisplay(staged?.value)}
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
