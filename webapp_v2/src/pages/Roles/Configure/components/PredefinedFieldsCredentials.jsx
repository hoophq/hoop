import { Stack } from '@mantine/core'
import SecretField from './SecretField'
import {
  decodeSecretValue,
  encodeSecretForSource,
  isSecretReference,
  SOURCES,
} from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'

// Renders a list of write-only SecretFields driven by a static field
// schema (key, label, required, placeholder, type). Used by every
// fixed-schema connection type — catalog databases, SSH, HTTP proxy,
// Claude Code, Kubernetes token. The caller is responsible for
// filtering / ordering fields appropriately (e.g. SSH's auth-method
// gating).
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
        const source = fieldSources[envKey] || defaultSource
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
              /^(_aws:|_envjson:|_vaultkv1:|_vaultkv2:|_aws_iam_rds:)/,
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
