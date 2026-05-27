import { useState } from 'react'
import { Stack, Title, Text, Anchor } from '@mantine/core'
import { FileText, Cloud, ShieldCheck, Info, ExternalLink } from 'lucide-react'
import Alert from '@/components/Alert'
import PageLoader from '@/components/PageLoader'
import SelectionCard from '@/components/SelectionCard'
import Select from '@/components/Select'
import PredefinedFieldsCredentials from './PredefinedFieldsCredentials'
import SSHCredentials from './SSHCredentials'
import CustomCredentials from './CustomCredentials'
import InsecureSslToggle from './InsecureSslToggle'
import {
  CONNECTION_METHODS,
  supportsConnectionMethods,
  supportsAwsIam,
} from '@/utils/connectionPolicy'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { deriveConnectionMethod } from '../utils/connectionMethod'
import { SOURCES, SOURCE_LABELS } from '../utils/secretsCodec'
import { useConfigureRoleStore } from '../store'

// Field schemas for connection types that don't ship credentials in
// connections-metadata.json. Co-located with the renderers that consume
// them so the data and the UI choice stay together. When these
// connection types gain credential entries in the JSON catalog, drop
// the matching constant and route through metadata like database
// catalog connections.
const HTTPPROXY_FIELDS = [
  {
    key: 'remote_url',
    label: 'Remote URL',
    required: true,
    placeholder: 'e.g. https://example.com',
  },
]

const CLAUDE_CODE_FIELDS = [
  {
    key: 'remote_url',
    label: 'Anthropic API URL',
    required: true,
    placeholder: 'https://api.anthropic.com',
  },
  {
    key: 'HEADER_X_API_KEY',
    label: 'Anthropic API Key',
    required: true,
    placeholder: 'sk-ant-...',
  },
]

const KUBERNETES_TOKEN_FIELDS = [
  {
    key: 'cluster_url',
    label: 'Cluster URL',
    required: true,
    placeholder: 'e.g. https://kubernetes.default.svc.cluster.local:443',
  },
  {
    key: 'authorization',
    label: 'Authorization token',
    required: true,
    placeholder: 'e.g. jwt.token.example',
  },
]

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

// Empty-state copy from the PRD, shown above every credentials section.
function WriteOnlyNotice() {
  return (
    <Alert variant="light" color="indigo" icon={<Info size={16} />}>
      Secret values are write-only. Once set, they cannot be viewed, only
      replaced or deleted. This matches the security model used by GitHub
      Actions and HashiCorp Vault.
    </Alert>
  )
}

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

// Renders a titled list of write-only credential fields. `fields` is
// the React field schema — supplied either by an inline constant (for
// connection types whose schema lives in this file) or by the metadata
// store (for catalog connection types).
function PredefinedSection({ title, fields, connection, availableSources, forceNewState }) {
  return (
    <Stack gap="md">
      <Title order={4}>{title}</Title>
      <PredefinedFieldsCredentials
        connection={connection}
        fields={fields}
        availableSources={availableSources}
        forceNewState={forceNewState}
      />
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

// Dispatch table — order matters: the first matching renderer wins.
// Mirrors the CLJS dispatch in credentials_tab.cljs (each branch maps
// to its own form id there). Add new connection shapes by appending an
// entry rather than nesting more if-clauses.
//
// `getSchema(subtype)` looks up the catalog credential schema from the
// metadata store. The `catalog-*` renderers depend on it; the others
// use inline schemas defined at the top of this file.
function buildRenderers(getSchema) {
  return [
    {
      name: 'database-catalog',
      match: (c) => c.type === 'database' && Boolean(getSchema(c.subtype)),
      render: (props) => (
        <PredefinedSection
          title="Environment credentials"
          fields={getSchema(props.connection.subtype)}
          {...props}
        />
      ),
    },
    {
      name: 'application-ssh',
      match: (c) =>
        c.type === 'application' && ['ssh', 'git', 'github'].includes(c.subtype),
      render: (props) => <SSHCredentials {...props} />,
    },
    {
      name: 'httpproxy-claude-code',
      match: (c) => c.type === 'httpproxy' && c.subtype === 'claude-code',
      render: (props) => (
        <Stack gap="xl">
          <PredefinedSection title="Basic info" fields={CLAUDE_CODE_FIELDS} {...props} />
          <InsecureSslToggle connection={props.connection} />
        </Stack>
      ),
    },
    {
      name: 'httpproxy-generic',
      match: (c) => c.type === 'httpproxy',
      render: (props) => (
        <Stack gap="xl">
          <PredefinedSection
            title="Environment credentials"
            fields={HTTPPROXY_FIELDS}
            {...props}
          />
          <InsecureSslToggle connection={props.connection} />
        </Stack>
      ),
    },
    {
      name: 'custom-kubernetes-token',
      match: (c) => c.type === 'custom' && c.subtype === 'kubernetes-token',
      render: (props) => (
        <Stack gap="xl">
          <PredefinedSection
            title="Kubernetes token"
            fields={KUBERNETES_TOKEN_FIELDS}
            {...props}
          />
          <InsecureSslToggle connection={props.connection} />
        </Stack>
      ),
    },
    {
      name: 'custom-catalog',
      match: (c) => c.type === 'custom' && Boolean(getSchema(c.subtype)),
      render: (props) => (
        <PredefinedSection
          title="Environment credentials"
          fields={getSchema(props.connection.subtype)}
          {...props}
        />
      ),
    },
    {
      name: 'custom-freeform',
      match: (c) => c.type === 'custom',
      render: (props) => (
        <CustomCredentials
          connection={props.connection}
          availableSources={props.availableSources}
        />
      ),
    },
  ]
}

// Whether the connection's renderer depends on the metadata catalog.
// When true and the catalog is still loading or errored, the body
// renders a loader / error rather than the renderer fallback.
function dependsOnCatalog(connection) {
  if (!connection) return false
  if (connection.type === 'database') return true
  if (connection.type === 'custom') {
    // Custom kubernetes-token uses inline fields; everything else
    // routes through catalog when matched.
    return connection.subtype !== 'kubernetes-token'
  }
  return false
}

function CredentialsBody({ connection, availableSources, forceNewState }) {
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
  return entry.render({ connection, availableSources, forceNewState })
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
  const showMethodCards = supportsConnectionMethods(connection)
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
      <WriteOnlyNotice />
      {showMethodCards && (
        <ConnectionMethodSection
          selectedMethod={selectedMethod}
          onSelect={setSelectedMethod}
          awsIamAvailable={awsIamAvailable}
        />
      )}
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
      />
    </Stack>
  )
}
