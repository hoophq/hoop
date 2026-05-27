import { useState } from 'react'
import { Stack, Title, Text, Anchor } from '@mantine/core'
import { FileText, Cloud, ShieldCheck, Info, ExternalLink } from 'lucide-react'
import Alert from '@/components/Alert'
import PageLoader from '@/components/PageLoader'
import SelectionCard from '@/components/SelectionCard'
import Select from '@/components/Select'
import {
  CONNECTION_METHODS,
  supportsAwsIam,
  isFreeFormCustomSubtype,
} from '@/utils/connectionPolicy'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { deriveConnectionMethod } from '../utils/connectionMethod'
import { SOURCES, SOURCE_LABELS } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'
import { buildRenderers } from './renderers'

const SECRETS_PROVIDERS = [
  SOURCES.VAULT_KV1,
  SOURCES.VAULT_KV2,
  SOURCES.AWS_SECRETS_MANAGER,
]

const METHOD_DEFINITIONS = [
  {
    id: CONNECTION_METHODS.MANUAL,
    title: 'Manual Input',
    description:
      'Enter credentials directly, including host, user, password, and other connection details.',
    icon: FileText,
  },
  {
    id: CONNECTION_METHODS.SECRETS_MANAGER,
    title: 'Secrets Manager',
    description:
      'Connect to a secrets provider like AWS Secrets Manager or HashiCorp Vault to automatically fetch your resource credentials.',
    icon: Cloud,
  },
  {
    id: CONNECTION_METHODS.AWS_IAM,
    title: 'AWS IAM Role',
    description:
      'Use an IAM Role that can be assumed to authenticate and access AWS resources.',
    icon: ShieldCheck,
  },
]

function ConnectionMethodSection({ selectedMethod, onSelect, awsIamAvailable }) {
  const visibleMethods = METHOD_DEFINITIONS.filter(
    (m) => m.id !== CONNECTION_METHODS.AWS_IAM || awsIamAvailable,
  )
  return (
    <Stack gap="md">
      <Title order={4}>Connection method</Title>
      <Stack gap="sm">
        {visibleMethods.map(({ id, title, description, icon }) => (
          <SelectionCard
            key={id}
            icon={icon}
            title={title}
            description={description}
            selected={selectedMethod === id}
            onClick={() => onSelect(id)}
          />
        ))}
      </Stack>
    </Stack>
  )
}

function UnsupportedFallback({ connection }) {
  return (
    <Alert variant="light" color="yellow" icon={<Info size={16} />}>
      <Stack gap={4}>
        <Text size="sm" fw={600}>
          {'Editing credentials for ' +
            (connection.subtype || connection.type) +
            ' connections is not yet available in the new editor.'}
        </Text>
        <Text size="sm">
          The write-only treatment still applies at the API level — values
          are never returned. Use the legacy editor to change credentials
          for this connection type.
        </Text>
      </Stack>
    </Alert>
  )
}

function MetadataError({ message }) {
  return (
    <Alert variant="light" color="red" icon={<Info size={16} />}>
      <Stack gap={4}>
        <Text size="sm" fw={600}>
          Could not load the connection catalog.
        </Text>
        <Text size="sm">{message}</Text>
      </Stack>
    </Alert>
  )
}

// True for connections whose final renderer can only be decided after
// the metadata catalog has loaded. While the catalog is still loading
// the body shows a loader for those; everything else (the five bespoke
// shapes and free-form custom fallbacks) renders immediately because
// its fields don't depend on the JSON.
function dependsOnCatalog(connection) {
  if (!connection) return false
  if (connection.type === 'database') return true
  if (connection.type === 'application') {
    return connection.subtype !== 'ssh'
  }
  if (connection.type === 'custom') {
    return (
      Boolean(connection.subtype) &&
      connection.subtype !== 'kubernetes-token' &&
      connection.subtype !== 'linux-vm' &&
      !isFreeFormCustomSubtype(connection.subtype)
    )
  }
  return false
}

function CredentialsBody({ connection, availableSources, forceNewState, connectionMethod }) {
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  const loading = useConnectionsMetadataStore((s) => s.loading)
  const error = useConnectionsMetadataStore((s) => s.error)
  const getCredentialSchema = useConnectionsMetadataStore(
    (s) => s.getCredentialSchema,
  )

  if (dependsOnCatalog(connection)) {
    if (loading && !metadata) return <PageLoader />
    if (error && !metadata) return <MetadataError message={error} />
  }

  const renderers = buildRenderers(getCredentialSchema)
  const entry = renderers.find((r) => r.match(connection))
  if (!entry) return <UnsupportedFallback connection={connection} />
  return entry.render({ connection, availableSources, forceNewState, connectionMethod })
}

function SecretsManagerProviderSection({ provider, onProviderChange }) {
  return (
    <Stack gap="xs">
      <Select
        label="Secrets manager provider"
        data={SECRETS_PROVIDERS.map((p) => ({ value: p, label: SOURCE_LABELS[p] }))}
        value={provider}
        onChange={(v) => v && onProviderChange(v)}
        allowDeselect={false}
      />
      <Anchor
        size="sm"
        href="https://hoop.dev/docs/setup/configuration/secrets-manager"
        target="_blank"
        rel="noopener noreferrer"
        display="inline-flex"
      >
        <ExternalLink size={12} />
        <Text component="span" ml={4}>Learn more about secrets manager setup</Text>
      </Anchor>
    </Stack>
  )
}

export default function CredentialsTab({ connection }) {
  const derivedMethod = deriveConnectionMethod(connection.secret)
  const [selectedMethod, setSelectedMethodState] = useState(derivedMethod)
  const [secretsProvider, setSecretsProvider] = useState(SOURCES.AWS_SECRETS_MANAGER)
  const clearStagedSecrets = useConfigureRoleStore((s) => s.clearStagedSecrets)
  // CLJS shows the picker on every credentials tab — see
  // server.cljs:43, server.cljs:137, server.cljs:186, network.cljs:34/84,
  // metadata_driven.cljs:121-139, claude_code_edit.cljs:59.
  const awsIamAvailable = supportsAwsIam(connection.subtype)
  const isSecretsManager = selectedMethod === CONNECTION_METHODS.SECRETS_MANAGER
  const isDerivedMethod = selectedMethod === derivedMethod

  // Switching method is a fresh start in write-only land: existing
  // "Set" cards become meaningless (the user can't peek at the value
  // they would re-encode), so we clear all fields and let the user
  // re-enter. Returning to the derived method drops the staged work,
  // which surfaces the original Set state from the loaded connection.
  const setSelectedMethod = (next) => {
    if (next === selectedMethod) return
    clearStagedSecrets()
    setSelectedMethodState(next)
  }

  // availableSources drives the per-field source-selector adornment.
  // In Secrets Manager mode the provider is the default; manual-input
  // is offered as the secondary option. AWS IAM mode renders no
  // adornment — the source is implicit.
  const availableSources = isSecretsManager
    ? [secretsProvider, SOURCES.MANUAL]
    : null

  // forceNewState tells field renderers to ignore the connection's
  // existing values (treat every field as empty). Active whenever the
  // user has switched away from the derived method.
  const forceNewState = !isDerivedMethod

  return (
    <Stack gap="xl" maw={720}>
      <ConnectionMethodSection
        selectedMethod={selectedMethod}
        onSelect={setSelectedMethod}
        awsIamAvailable={awsIamAvailable}
      />
      {isSecretsManager && (
        <SecretsManagerProviderSection
          provider={secretsProvider}
          onProviderChange={setSecretsProvider}
        />
      )}
      <CredentialsBody
        connection={connection}
        availableSources={availableSources}
        forceNewState={forceNewState}
        connectionMethod={selectedMethod}
      />
    </Stack>
  )
}
