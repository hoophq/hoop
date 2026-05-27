import { decodeSecretValue, encodeSecretValue } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'
import ToggleSection from './ToggleSection'

// "Allow insecure SSL" toggle backed by envvar:INSECURE.
//
// The backend exempts boolean-looking values from write-only stripping
// (see gateway/api/connections/secrets.go::isBooleanValue), so we can
// read the actual current state instead of starting blind. Toggling
// stages a replace whose value is base64-encoded "true" or "false".
export default function InsecureSslToggle({ connection }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)

  const envKey = 'envvar:INSECURE'
  const staged = stagedSecrets[envKey]
  const stored = connection.secret?.[envKey]

  const currentValue = (() => {
    if (staged) return decodeSecretValue(staged.value)
    if (stored) return decodeSecretValue(stored)
    return 'false'
  })()
  const checked = currentValue === 'true'

  return (
    <ToggleSection
      title="Allow insecure SSL"
      description="Skip SSL certificate verification for HTTPS connections."
      checked={checked}
      onChange={(next) =>
        replaceSecret(envKey, encodeSecretValue(next ? 'true' : 'false'))
      }
    />
  )
}
