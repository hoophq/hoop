import { Stack } from '@mantine/core'
import SecretField from './SecretField'
import { decodeSecretValue, encodeSecretValue, isSecretReference } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'

// Renders a list of write-only SecretFields driven by a static field
// schema (key, label, required, placeholder, type). Used by every
// fixed-schema connection type — catalog databases, SSH, HTTP proxy,
// Claude Code, Kubernetes token. The caller is responsible for
// filtering / ordering fields appropriately (e.g. SSH's auth-method
// gating).
export default function PredefinedFieldsCredentials({ connection, fields, isAdmin }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)

  const currentSecrets = connection.secret || {}

  return (
    <Stack gap="lg">
      {fields.map((field) => {
        const envKey = `envvar:${field.key.toUpperCase()}`
        const encodedValue = currentSecrets[envKey]
        const isExisting =
          envKey in currentSecrets &&
          (encodedValue !== '' || connection.secrets_updated_at != null)
        const isReference = isSecretReference(encodedValue)
        const referenceText = isReference ? decodeSecretValue(encodedValue) : ''
        const staged = stagedSecrets[envKey]
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
            stagedValue={staged?.value ? decodeSecretValue(staged.value) : ''}
            secretsUpdatedAt={connection.secrets_updated_at}
            onReplace={(plain) =>
              isAdmin && replaceSecret(envKey, encodeSecretValue(plain))
            }
            onChangeStaged={(plain) =>
              isAdmin && replaceSecret(envKey, encodeSecretValue(plain))
            }
            onCancel={() => cancelSecretChange(envKey)}
          />
        )
      })}
    </Stack>
  )
}
