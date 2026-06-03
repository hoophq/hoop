import { useState } from 'react'
import { Stack, Title, Text, Anchor } from '@mantine/core'
import { FileText, Cloud, ShieldCheck, TriangleAlert, ExternalLink } from 'lucide-react'
import Alert from '@/components/Alert'
import PageLoader from '@/components/PageLoader'
import SelectionCard from '@/components/SelectionCard'
import Select from '@/components/Select'
import {
  CONNECTION_METHODS,
  supportsAwsIam,
} from '@/utils/connectionPolicy'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { deriveConnectionInfo } from './utils/connectionMethod'
import { SOURCES, SOURCE_LABELS } from './utils/secretsCodec'
import { useConfigureRoleStore } from './store'
import { pickRendererRule } from './sections/credentials'

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

function CredentialsError({ title, message }) {
  return (
    <Alert variant="light" color="red" icon={<TriangleAlert size={16} />}>
      <Stack gap={4}>
        <Text size="sm" fw={600}>
          {title}
        </Text>
        {message && <Text size="sm">{message}</Text>}
      </Stack>
    </Alert>
  )
}

function CredentialsBody({ connection, availableSources, forceNewState, connectionMethod }) {
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  const loading = useConnectionsMetadataStore((s) => s.loading)
  const error = useConnectionsMetadataStore((s) => s.error)
  const getCredentialSchema = useConnectionsMetadataStore(
    (s) => s.getCredentialSchema,
  )

  const shape = connection.subtype
    ? connection.type + '/' + connection.subtype
    : connection.type

  const rule = pickRendererRule(connection)
  if (!rule) {
    return (
      <CredentialsError
        title={`No credential editor is registered for ${shape}.`}
      />
    )
  }

  if (rule.requiresCatalog) {
    if (loading && !metadata) return <PageLoader />
    if (error && !metadata) {
      return (
        <CredentialsError
          title="Could not load the connection catalog."
          message={error}
        />
      )
    }
  }

  const node = rule.render(
    { connection, availableSources, forceNewState, connectionMethod },
    { getSchema: getCredentialSchema },
  )
  if (!node) {
    return (
      <CredentialsError
        title={`No catalog entry for ${shape}.`}
        message="Try refreshing the page — if it keeps failing, the connections catalog is missing this subtype."
      />
    )
  }
  return node
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
  // Derive method + provider from existing values so the top-level
  // controls reflect what's actually stored, not a hardcoded default.
  const derived = deriveConnectionInfo(connection.secret)
  const [selectedMethod, setSelectedMethodState] = useState(derived.method)
  const [secretsProvider, setSecretsProviderState] = useState(
    derived.provider || SOURCES.AWS_SECRETS_MANAGER,
  )
  const switchConnectionMethod = useConfigureRoleStore(
    (s) => s.switchConnectionMethod,
  )
  const setSecretsManagerProvider = useConfigureRoleStore(
    (s) => s.setSecretsManagerProvider,
  )
  const awsIamAvailable = supportsAwsIam(connection.subtype)
  const isSecretsManager = selectedMethod === CONNECTION_METHODS.SECRETS_MANAGER
  const isDerivedMethod = selectedMethod === derived.method

  // Method switch wipes existing fields and stages deletes for any
  // surviving reference that doesn't belong to the new method.
  const setSelectedMethod = (next) => {
    if (next === selectedMethod) return
    switchConnectionMethod(next)
    setSelectedMethodState(next)
  }

  // Provider switch re-encodes every existing reference under the new
  // prefix so the dropdown choice actually survives save + reload.
  const setSecretsProvider = (next) => {
    if (next === secretsProvider) return
    setSecretsManagerProvider(next)
    setSecretsProviderState(next)
  }

  // In Secrets Manager mode every row gets the per-field source picker.
  // Manual / AWS IAM render no per-field adornment.
  const availableSources = isSecretsManager
    ? [secretsProvider, SOURCES.MANUAL]
    : null

  // Render every field as fresh whenever the user switched away from
  // the derived method — the persisted values no longer apply.
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
