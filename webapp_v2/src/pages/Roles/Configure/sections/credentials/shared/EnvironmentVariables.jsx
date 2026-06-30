import { useEffect, useState } from 'react'
import { Stack, Title, Text, Grid } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import ActionIcon from '@/components/ActionIcon'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import SourcedInput from '@/components/SourcedInput'
import SecretField from '../../../components/SecretField'
import {
  decodeForDisplay,
  encodeSecretForSource,
  isSecretReference,
  isValidPosixKey,
  sourceFromEncodedValue,
  PLACEHOLDER_KEY_RE,
  SOURCES,
} from '../../../utils/secretsCodec'
import { useConfigureRoleStore } from '../../../store'
import { sourceOptionsFor } from '../../../components/SecretField/util'

// Free-form env-var editor. Values round-trip plaintext from the
// backend; rename commits on blur and is translated into delete-old +
// replace-new at save time so the row's rendered position stays put.
// The list never shrinks past one empty row — auto-adds a placeholder
// if everything was removed.
//
// When `availableSources` is supplied (Secrets Manager mode) each row
// gets a per-row source picker. The picked source decides whether the
// value is encoded bare (manual) or with a `_vaultkv1:` / `_aws:` /
// `_vaultkv2:` prefix.

function EnvvarRow({
  rowKey,
  displayName,
  value,
  source,
  availableSources,
  writeOnly,
  stagedAction,
  onCommitKey,
  onValueChange,
  onSourceChange,
  onCancelReplace,
  onRemove,
}) {
  const [draftName, setDraftName] = useState(displayName)

  // Keep the local draft in sync when the displayed name changes from
  // outside (e.g. another component triggered a rename for this key).
  useEffect(() => {
    setDraftName(displayName)
  }, [displayName])

  const showSourceSelect = Boolean(availableSources)

  return (
    <Grid gutter="md" align="flex-end" key={rowKey}>
      <Grid.Col span={5}>
        <TextInput
          label="Key"
          value={draftName}
          onChange={(e) => {
            const next = e.currentTarget.value
            // Live POSIX validation — invalid characters are silently
            // rejected so the user can't end up with `12_BAD` (which
            // the agent rejects at connect time anyway).
            if (isValidPosixKey(next)) setDraftName(next)
          }}
          onBlur={() => {
            const trimmed = draftName.trim()
            if (!trimmed) return
            onCommitKey(trimmed)
          }}
          placeholder="e.g. API_KEY"
        />
      </Grid.Col>
      <Grid.Col span={6}>
        {writeOnly ? (
          <SecretField
            label="Value"
            type="password"
            isExisting
            stagedAction={stagedAction}
            stagedValue={value}
            source={source}
            availableSources={availableSources}
            onSourceChange={onSourceChange}
            onReplace={onValueChange}
            onChangeStaged={onValueChange}
            onCancel={onCancelReplace}
          />
        ) : showSourceSelect ? (
          <SourcedInput
            label="Value"
            type="password"
            placeholder="Enter value"
            value={value}
            onChange={onValueChange}
            source={source}
            sources={sourceOptionsFor(availableSources)}
            onSourceChange={onSourceChange}
          />
        ) : (
          <PasswordInput
            label="Value"
            value={value}
            onChange={(e) => onValueChange(e.currentTarget.value)}
            placeholder="Enter value"
          />
        )}
      </Grid.Col>
      <Grid.Col span={1}>
        <ActionIcon
          variant="subtle"
          color="red"
          size={36}
          onClick={onRemove}
          aria-label={'Remove ' + displayName}
        >
          <Trash2 size={16} />
        </ActionIcon>
      </Grid.Col>
    </Grid>
  )
}

export default function EnvironmentVariables({ connection, availableSources, hideRoleInfo }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const fieldSources = useConfigureRoleStore((s) => s.fieldSources)
  const renames = useConfigureRoleStore((s) => s.renames)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const renameSecret = useConfigureRoleStore((s) => s.renameSecret)
  const setFieldSource = useConfigureRoleStore((s) => s.setFieldSource)

  // When the parent runs in Secrets Manager mode it passes
  // [provider, manual-input] as availableSources. The provider is the
  // first item so it doubles as the default for fresh rows.
  const defaultSource = availableSources?.[0] || SOURCES.MANUAL

  const currentSecrets = connection.secret || {}
  const stagedDeletedKeys = new Set(
    Object.entries(stagedSecrets)
      .filter(([, change]) => change.action === 'delete')
      .map(([k]) => k),
  )
  const existingKeys = Object.keys(currentSecrets)
    .filter((k) => k.startsWith('envvar:'))
    .filter((k) => !stagedDeletedKeys.has(k))
  const stagedNewKeys = Object.entries(stagedSecrets)
    .filter(
      ([k, change]) =>
        change.action === 'new' &&
        k.startsWith('envvar:') &&
        !existingKeys.includes(k),
    )
    .map(([k]) => k)
  const allKeys = [...existingKeys, ...stagedNewKeys]

  // Keep at least one row available so the section never collapses.
  // Matches CLJS behaviour (the legacy form always shows a blank input).
  useEffect(() => {
    if (allKeys.length === 0) {
      const sentinel = 'envvar:NEW_KEY_1'
      replaceSecret(sentinel, '')
    }
  }, [allKeys.length, replaceSecret])

  const addEmptyRow = () => {
    let i = 1
    while (allKeys.includes(`envvar:NEW_KEY_${i}`)) i += 1
    replaceSecret(`envvar:NEW_KEY_${i}`, '')
  }

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={4}>Environment variables</Title>
        <Text size="sm" c="dimmed">
          Include environment variables to be used in your resource role.
        </Text>
      </Stack>

      <Stack gap="md">
        {allKeys.map((envKey) => {
          const staged = stagedSecrets[envKey]
          const isExisting = envKey in currentSecrets
          const renamedTo = renames[envKey]
          const effectiveKey = renamedTo || envKey
          // Hide the auto-generated `NEW_KEY_N` sentinel from the user
          // ONLY when it's an actual UI placeholder (not persisted on
          // the connection). Legacy junk records with the same name
          // shape — created by an older version that didn't reject
          // unnamed placeholders on save — must still display so the
          // user can see and clean them up.
          const isPlaceholder = !isExisting && PLACEHOLDER_KEY_RE.test(effectiveKey)
          const displayName = isPlaceholder ? '' : effectiveKey.slice('envvar:'.length)
          // If anything is staged for this row we honour it verbatim,
          // even when the value is an empty string — that's "user
          // explicitly cleared the input" and we don't want to fall back
          // to the persisted value (would resurrect stale data when the
          // auto-placeholder kicks in after a delete).
          //
          // We also strip any leading provider prefix (`_vaultkv1:` etc)
          // so the input shows the bare reference id; the source picker
          // to the left conveys which provider applies. In Manual mode
          // (no availableSources) the strip is a no-op for ordinary
          // values and only affects rows that happened to be saved as
          // references — those round-trip back through the picker once
          // the user re-enters Secrets Manager mode.
          const value = staged
            ? decodeForDisplay(staged.value || '')
            : decodeForDisplay(currentSecrets[envKey])
          // Source priority: explicit per-field pick (fieldSources) →
          // detection from the encoded value → defaultSource (provider
          // when in Secrets Manager mode, manual otherwise).
          const encodedForDetection = staged
            ? staged.value
            : currentSecrets[envKey] || ''
          const source =
            fieldSources[envKey] ||
            (encodedForDetection ? sourceFromEncodedValue(encodedForDetection) : null) ||
            defaultSource
          const isPersistedReference = isSecretReference(currentSecrets[envKey] || '')
          const writeOnly = Boolean(hideRoleInfo) && isExisting && !isPersistedReference
          return (
            <EnvvarRow
              key={envKey}
              rowKey={envKey}
              displayName={displayName}
              value={value}
              source={source}
              availableSources={availableSources}
              writeOnly={writeOnly}
              stagedAction={staged?.action}
              onCommitKey={(newName) => {
                const nextKey = newName.startsWith('envvar:')
                  ? newName
                  : 'envvar:' + newName.toUpperCase()
                renameSecret(envKey, nextKey)
              }}
              onValueChange={(plain) =>
                replaceSecret(envKey, encodeSecretForSource(plain, source))
              }
              onSourceChange={(nextSource) => {
                // Stage the value under the new source before bumping
                // fieldSources so save() always sends the right prefix —
                // even when the user just toggles the picker without
                // typing. setFieldSource handles re-encoding for staged
                // rows; for not-yet-staged rows we stage their current
                // (already-stripped) value here.
                if (!staged) {
                  replaceSecret(envKey, encodeSecretForSource(value, nextSource))
                }
                setFieldSource(envKey, nextSource)
              }}
              onCancelReplace={() => cancelSecretChange(envKey)}
              onRemove={() => {
                if (isExisting) deleteSecret(envKey)
                else cancelSecretChange(envKey)
              }}
            />
          )
        })}
        <Button
          variant="light"
          leftSection={<Plus size={14} />}
          w="fit-content"
          onClick={addEmptyRow}
        >
          Add key/value
        </Button>
      </Stack>
    </Stack>
  )
}
