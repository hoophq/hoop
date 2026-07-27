import { useEffect } from 'react'
import { Stack } from '@mantine/core'
import SourcedInput from '@/components/SourcedInput'
import SecretField from '@/pages/Roles/Configure/components/SecretField'
import { sourceOptionsFor } from '@/pages/Roles/Configure/components/SecretField/util'
import {
  decodeForDisplay,
  decodeSecretValue,
  encodeSecretForSource,
  isSecretReference,
  sourceFromEncodedValue,
  SOURCES,
} from '@/pages/Roles/Configure/utils/secretsCodec'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'

const AWS_IAM_PASS_VALUE = 'authtoken'
const AWS_IAM_PASS_ENCODED = encodeSecretForSource(
  AWS_IAM_PASS_VALUE,
  SOURCES.AWS_IAM_ROLE,
)

// Schema-driven credential row list. A row renders as a SecretField
// "Set / Replace" gate when the backend returned an empty value (an
// inline secret the gateway stripped because the org has hide_role_info
// on), or -- defensively -- for any non-reference value while
// hide_role_info is on. Everything else round-trips as an editable
// SourcedInput: provider references always, plus every inline value when
// hide_role_info is off.
//
// In AWS IAM Role mode (postgres/mysql), PASS is hidden and force-staged
// to `_aws_iam_rds:authtoken`; USER and PASS get the `_aws_iam_rds:`
// prefix on save. Mirrors CLJS process_form.cljs:113-138.
//
// `forceNewState` is set by CredentialsTab after a method switch so
// every field renders empty regardless of the persisted value.
export default function PredefinedFields({
  connection,
  fields,
  availableSources,
  forceNewState,
  connectionMethod,
  hideRoleInfo,
}) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const fieldSources = useConfigureRoleStore((s) => s.fieldSources)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const setFieldSource = useConfigureRoleStore((s) => s.setFieldSource)

  const isAwsIam = connectionMethod === CONNECTION_METHODS.AWS_IAM
  const currentSecrets = connection.secret || {}
  const defaultSource = availableSources?.[0] || SOURCES.MANUAL

  // AWS IAM mode forces PASS=`_aws_iam_rds:authtoken` and hides the
  // field; stage it here so save() picks it up. Skipped when the
  // persisted value already matches.
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

  const visibleFields = isAwsIam
    ? fields.filter((f) => f.key.toLowerCase() !== 'pass')
    : fields

  return (
    <Stack gap="lg">
      {visibleFields.map((field) => {
        const envKey = `envvar:${field.key.toUpperCase()}`
        const encodedValue = currentSecrets[envKey]
        // Key presence is the existence signal — the gateway preserves
        // the key set when it strips inline values, so a present key
        // means the credential exists on the server.
        const isExisting = !forceNewState && envKey in currentSecrets
        const isReference = !forceNewState && isSecretReference(encodedValue)
        const referenceText = isReference ? decodeSecretValue(encodedValue) : ''
        const staged = stagedSecrets[envKey]
        // Priority: explicit pick → detection from prefix → mode default.
        const encodedForDetection = staged ? staged.value : encodedValue || ''
        const detectedSource =
          fieldSources[envKey] ||
          (encodedForDetection ? sourceFromEncodedValue(encodedForDetection) : null) ||
          defaultSource
        // AWS IAM mode forces the `_aws_iam_rds:` source on USER + PASS.
        const fieldKeyLower = field.key.toLowerCase()
        const isAwsIamScoped = isAwsIam && (fieldKeyLower === 'user' || fieldKeyLower === 'pass')
        const source = isAwsIamScoped ? SOURCES.AWS_IAM_ROLE : detectedSource

        // Any value the backend round-tripped renders editable; empty
        // values (Manual secrets the gateway stripped) fall through to
        // the SecretField gate below.
        const isRoundTrippedPlain =
          !forceNewState && encodedValue && !staged && (!hideRoleInfo || isReference)
        if (isRoundTrippedPlain) {
          const decodedValue = decodeForDisplay(encodedValue)
          return (
            <SourcedInput
              key={envKey}
              label={field.label}
              required={field.required}
              placeholder={field.placeholder}
              type={field.type}
              minRows={field.minRows}
              value={decodedValue}
              onChange={(plain) =>
                replaceSecret(envKey, encodeSecretForSource(plain, source))
              }
              source={source}
              sources={sourceOptionsFor(availableSources)}
              onSourceChange={(nextSource) => {
                // Re-stage under the new prefix so save() sends the
                // right encoding even if the user just toggled the
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
            minRows={field.minRows}
            isExisting={isExisting}
            isReference={isReference}
            referenceText={referenceText}
            allowDelete={false}
            stagedAction={staged?.action}
            stagedValue={decodeForDisplay(staged?.value)}
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
