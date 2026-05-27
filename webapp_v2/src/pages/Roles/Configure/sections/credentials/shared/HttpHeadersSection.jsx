import { useEffect, useState } from 'react'
import { Stack, Title, Text, Grid } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import ActionIcon from '@/components/ActionIcon'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import SourcedInput from '@/components/SourcedInput'
import {
  decodeForDisplay,
  encodeSecretForSource,
  isValidHeaderKey,
  sourceFromEncodedValue,
  PLACEHOLDER_KEY_RE,
  SOURCES,
} from '../../../utils/secretsCodec'
import { useConfigureRoleStore } from '../../../store'
import { sourceOptionsFor } from '../../../components/SecretField/util'

const HEADER_PREFIX = 'envvar:HEADER_'

// HTTP headers editor for httpproxy connections. Same row shape as
// EnvironmentVariablesSection but keyed under `envvar:HEADER_*`. CLJS
// reference: configuration_inputs.cljs::http-headers-section
// (parse-header-key allows any non-whitespace value — headers can be
// case-sensitive and contain hyphens, so we don't enforce POSIX
// uppercase here, unlike env vars).

function HeaderRow({
  rowKey,
  displayName,
  value,
  source,
  availableSources,
  onCommitKey,
  onValueChange,
  onSourceChange,
  onRemove,
}) {
  const [draftName, setDraftName] = useState(displayName)

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
            // Headers allow any non-whitespace string (case-sensitive)
            // — mirrors CLJS configuration_inputs.cljs:16-17,81-97.
            if (isValidHeaderKey(next)) setDraftName(next)
          }}
          onBlur={() => {
            const trimmed = draftName.trim()
            if (!trimmed) return
            onCommitKey(trimmed)
          }}
          placeholder="X-Request-Id"
        />
      </Grid.Col>
      <Grid.Col span={6}>
        {showSourceSelect ? (
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
          size="lg"
          onClick={onRemove}
          aria-label={'Remove ' + displayName}
        >
          <Trash2 size={16} />
        </ActionIcon>
      </Grid.Col>
    </Grid>
  )
}

// `excludeKeys` lists envvar keys this section should ignore — used by
// ClaudeCodeRenderer to hide HEADER_X_API_KEY (which has its own input
// in the Basic info section).
export default function HttpHeadersSection({
  connection,
  availableSources,
  excludeKeys = [],
}) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const fieldSources = useConfigureRoleStore((s) => s.fieldSources)
  const renames = useConfigureRoleStore((s) => s.renames)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)
  const renameSecret = useConfigureRoleStore((s) => s.renameSecret)
  const setFieldSource = useConfigureRoleStore((s) => s.setFieldSource)

  const defaultSource = availableSources?.[0] || SOURCES.MANUAL
  const exclude = new Set(excludeKeys)

  const currentSecrets = connection.secret || {}
  const stagedDeletedKeys = new Set(
    Object.entries(stagedSecrets)
      .filter(([, change]) => change.action === 'delete')
      .map(([k]) => k),
  )
  const existingKeys = Object.keys(currentSecrets)
    .filter((k) => k.startsWith(HEADER_PREFIX))
    .filter((k) => !exclude.has(k))
    .filter((k) => !stagedDeletedKeys.has(k))
  const stagedNewKeys = Object.entries(stagedSecrets)
    .filter(
      ([k, change]) =>
        change.action === 'new' &&
        k.startsWith(HEADER_PREFIX) &&
        !exclude.has(k) &&
        !existingKeys.includes(k),
    )
    .map(([k]) => k)
  const allKeys = [...existingKeys, ...stagedNewKeys]

  // Keep at least one row available so the section never collapses.
  // Matches CLJS behaviour and EnvironmentVariablesSection — the list
  // always shows a blank input even when no headers exist yet.
  useEffect(() => {
    if (allKeys.length === 0) {
      replaceSecret(`${HEADER_PREFIX}NEW_HEADER_1`, '')
    }
  }, [allKeys.length, replaceSecret])

  const addEmptyRow = () => {
    let i = 1
    while (allKeys.includes(`${HEADER_PREFIX}NEW_HEADER_${i}`)) i += 1
    replaceSecret(`${HEADER_PREFIX}NEW_HEADER_${i}`, '')
  }

  return (
    <Stack gap="md">
      <Stack gap="xs">
        <Title order={4}>HTTP headers</Title>
        <Text size="sm" c="dimmed">
          Add HTTP headers that will be sent with each proxied request.
        </Text>
      </Stack>

      <Stack gap="md">
        {allKeys.map((envKey) => {
          const staged = stagedSecrets[envKey]
          const isExisting = envKey in currentSecrets
          const renamedTo = renames[envKey]
          const effectiveKey = renamedTo || envKey
          const isPlaceholder =
            !isExisting && PLACEHOLDER_KEY_RE.test(effectiveKey)
          const displayName = isPlaceholder
            ? ''
            : effectiveKey.slice(HEADER_PREFIX.length)
          const value = staged
            ? decodeForDisplay(staged.value || '')
            : decodeForDisplay(currentSecrets[envKey])
          const encodedForDetection = staged
            ? staged.value
            : currentSecrets[envKey] || ''
          const source =
            fieldSources[envKey] ||
            (encodedForDetection ? sourceFromEncodedValue(encodedForDetection) : null) ||
            defaultSource
          return (
            <HeaderRow
              key={envKey}
              rowKey={envKey}
              displayName={displayName}
              value={value}
              source={source}
              availableSources={availableSources}
              onCommitKey={(newName) => {
                // Header names round-trip case-sensitive — don't
                // uppercase like env vars do.
                const nextKey = newName.startsWith(HEADER_PREFIX)
                  ? newName
                  : HEADER_PREFIX + newName
                renameSecret(envKey, nextKey)
              }}
              onValueChange={(plain) =>
                replaceSecret(envKey, encodeSecretForSource(plain, source))
              }
              onSourceChange={(nextSource) => {
                if (!staged) {
                  replaceSecret(envKey, encodeSecretForSource(value, nextSource))
                }
                setFieldSource(envKey, nextSource)
              }}
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
          Add header
        </Button>
      </Stack>
    </Stack>
  )
}
