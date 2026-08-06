import { useEffect, useState } from 'react'
import { Stack, Title, Text, Grid } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import ActionIcon from '@/components/ActionIcon'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import SourcedInput from '@/components/SourcedInput'
import SecretField from '@/pages/Roles/Configure/components/SecretField'
import {
  decodeForDisplay,
  encodeSecretForSource,
  isSecretReference,
  isValidHeaderKey,
  sourceFromEncodedValue,
  PLACEHOLDER_KEY_RE,
  SOURCES,
} from '@/pages/Roles/Configure/utils/secretsCodec'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'
import { sourceOptionsFor } from '@/pages/Roles/Configure/components/SecretField/util'

// HTTP headers editor for httpproxy connections. Same row shape as
// EnvironmentVariables but keyed under `envvar:HEADER_*`. CLJS
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
  writeOnly,
  stagedAction,
  keyPlaceholder,
  onCommitKey,
  onValueChange,
  onSourceChange,
  onCancelReplace,
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
          placeholder={keyPlaceholder}
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
//
// `prefix` and the copy props retarget the same editor at a different
// carve-out namespace. A stdio MCP Gateway server needs its secrets in the
// child process's environment, which the agent collects from `MCPENV_*`
// (agent/controller/mcpproxy.go::stripPrefixKeys) — the same rows, the same
// rename-on-blur, a different prefix. Duplicating the component to change one
// string is how the two copies drift.
export default function HttpHeaders({
  connection,
  availableSources,
  excludeKeys = [],
  hideRoleInfo,
  prefix = 'envvar:HEADER_',
  title = 'HTTP headers',
  description = 'Add HTTP headers that will be sent with each proxied request.',
  keyPlaceholder = 'X-Request-Id',
  addLabel = 'Add header',
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
    .filter((k) => k.startsWith(prefix))
    .filter((k) => !exclude.has(k))
    .filter((k) => !stagedDeletedKeys.has(k))
  const stagedNewKeys = Object.entries(stagedSecrets)
    .filter(
      ([k, change]) =>
        change.action === 'new' &&
        k.startsWith(prefix) &&
        !exclude.has(k) &&
        !existingKeys.includes(k),
    )
    .map(([k]) => k)
  const allKeys = [...existingKeys, ...stagedNewKeys]

  // Keep at least one row available so the section never collapses.
  // Matches CLJS behaviour and EnvironmentVariables — the list
  // always shows a blank input even when no headers exist yet.
  useEffect(() => {
    if (allKeys.length === 0) {
      replaceSecret(`${prefix}NEW_HEADER_1`, '')
    }
  }, [allKeys.length, prefix, replaceSecret])

  const addEmptyRow = () => {
    let i = 1
    while (allKeys.includes(`${prefix}NEW_HEADER_${i}`)) i += 1
    replaceSecret(`${prefix}NEW_HEADER_${i}`, '')
  }

  return (
    <Stack gap="md">
      <Stack gap="xs">
        <Title order={4}>{title}</Title>
        <Text size="sm" c="dimmed">
          {description}
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
            : effectiveKey.slice(prefix.length)
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
          const isPersistedReference = isSecretReference(currentSecrets[envKey] || '')
          const writeOnly = Boolean(hideRoleInfo) && isExisting && !isPersistedReference
          return (
            <HeaderRow
              key={envKey}
              rowKey={envKey}
              displayName={displayName}
              value={value}
              source={source}
              availableSources={availableSources}
              writeOnly={writeOnly}
              stagedAction={staged?.action}
              keyPlaceholder={keyPlaceholder}
              onCommitKey={(newName) => {
                // Header names round-trip case-sensitive — don't
                // uppercase like env vars do.
                const nextKey = newName.startsWith(prefix)
                  ? newName
                  : prefix + newName
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
          {addLabel}
        </Button>
      </Stack>
    </Stack>
  )
}
