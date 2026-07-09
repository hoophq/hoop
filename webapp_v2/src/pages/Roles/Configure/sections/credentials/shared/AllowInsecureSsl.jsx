import { useState } from 'react'
import { Group, Stack, Title, Text, ThemeIcon } from '@mantine/core'
import { EyeOff } from 'lucide-react'
import Switch from '@/components/Switch'
import ToggleSection from '@/pages/Roles/Configure/components/ToggleSection'
import InlineAction from '@/pages/Roles/Configure/components/SecretField/InlineAction'
import { decodeSecretValue, encodeSecretValue } from '@/pages/Roles/Configure/utils/secretsCodec'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'

const ENV_KEY = 'envvar:INSECURE'
const TITLE = 'Allow insecure SSL'
const DESCRIPTION = 'Skip SSL certificate verification for HTTPS connections.'

// Title + description block shared by both write-only states. Only the
// left element (status icon vs. live switch) and the trailing action
// (Replace vs. Restore) differ between them; the layout mirrors
// ToggleSection so all three states read as the same row.
function MaskedRow({ left, action }) {
  return (
    <Group align="center" gap="md" wrap="nowrap">
      {left}
      <Stack gap="xs" flex={1}>
        <Title order={5} fw={500}>
          {TITLE}
        </Title>
        <Text size="sm" c="dimmed">
          {DESCRIPTION}
        </Text>
      </Stack>
      {action}
    </Group>
  )
}

// "Allow insecure SSL" toggle, backed by envvar:INSECURE.
//
// In write-only mode the backend masks every inline secret value —
// including this boolean — so the API returns the key with an empty value
// and we can no longer tell whether SSL verification is on or off. A plain
// switch would have to assert a position we don't actually know, so the
// field adopts the same Replace-to-reveal contract as every other masked
// secret (see components/SecretField): the stored value stays hidden until
// the user chooses to replace it, and an untouched field is preserved on
// save (buildSecretsPatch echoes it back as null).
//
// When the value IS readable — write-only off, or a brand-new connection —
// it stays the ordinary ToggleSection.
export default function AllowInsecureSsl({ connection }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const hideRoleInfo = useConfigureRoleStore((s) => s.hideRoleInfo)
  const [revealed, setRevealed] = useState(false)

  const staged = stagedSecrets[ENV_KEY]
  const isExisting = Boolean(connection.secret && ENV_KEY in connection.secret)
  const stored = connection.secret?.[ENV_KEY]
  // Masked == the key exists but the backend stripped its value.
  const masked = hideRoleInfo && isExisting && !stored

  const setValue = (on) =>
    replaceSecret(ENV_KEY, encodeSecretValue(on ? 'true' : 'false'))

  // Masked and untouched: don't show a switch (its position would be a
  // guess), just the Replace affordance.
  if (masked && !staged && !revealed) {
    return (
      <MaskedRow
        left={
          <ThemeIcon size="md" radius="xl" variant="light" color="gray">
            <EyeOff size={14} />
          </ThemeIcon>
        }
        action={<InlineAction kind="replace" onClick={() => setRevealed(true)} />}
      />
    )
  }

  const checked = decodeSecretValue(staged ? staged.value : stored) === 'true'

  // Masked and replacing: a live switch (starts off — no prior value is
  // assumed) plus Restore to drop the change and keep the stored value.
  if (masked) {
    return (
      <MaskedRow
        left={
          <Switch
            size="md"
            checked={checked}
            onChange={(e) => setValue(e.currentTarget.checked)}
            aria-label={TITLE}
          />
        }
        action={
          <InlineAction
            kind="restore"
            onClick={() => {
              setRevealed(false)
              cancelSecretChange(ENV_KEY)
            }}
          />
        }
      />
    )
  }

  return (
    <ToggleSection
      title={TITLE}
      description={DESCRIPTION}
      checked={checked}
      onChange={setValue}
    />
  )
}
