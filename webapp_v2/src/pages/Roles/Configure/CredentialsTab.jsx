import { useState } from 'react'
import { Group, Stack, Title, Text, Anchor } from '@mantine/core'
import { FileText, Cloud, ShieldCheck, Info, ExternalLink } from 'lucide-react'
import Alert from '@/components/Alert'
import PageLoader from '@/components/PageLoader'
import SelectionCard from '@/components/SelectionCard'
import Select from '@/components/Select'
import Switch from '@/components/Switch'
import {
  SourcedInputVariantProvider,
  VARIANT_SINGLE_OUTLINE,
  VARIANT_GLUED_SIBLINGS,
} from '@/components/SourcedInput/variantContext'
import {
  CONNECTION_METHODS,
  supportsAwsIam,
} from '@/utils/connectionPolicy'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { deriveConnectionMethod } from './utils/connectionMethod'
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

function CredentialsBody({ connection, availableSources, forceNewState, connectionMethod }) {
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  const loading = useConnectionsMetadataStore((s) => s.loading)
  const error = useConnectionsMetadataStore((s) => s.error)
  const getCredentialSchema = useConnectionsMetadataStore(
    (s) => s.getCredentialSchema,
  )

  const rule = pickRendererRule(connection)
  if (!rule) return <UnsupportedFallback connection={connection} />

  if (rule.requiresCatalog) {
    if (loading && !metadata) return <PageLoader />
    if (error && !metadata) return <MetadataError message={error} />
  }

  const node = rule.render(
    { connection, availableSources, forceNewState, connectionMethod },
    { getSchema: getCredentialSchema },
  )
  if (!node) return <UnsupportedFallback connection={connection} />
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
  const derivedMethod = deriveConnectionMethod(connection.secret)
  const [selectedMethod, setSelectedMethodState] = useState(derivedMethod)
  const [secretsProvider, setSecretsProvider] = useState(SOURCES.AWS_SECRETS_MANAGER)
  // TEMPORARY: A/B switch for the SourcedInput redesign. Toggling flips
  // the picker visual for every field on the page so we can compare the
  // two layouts side-by-side. Remove this useState + the Switch below
  // + the SourcedInputVariantProvider wrapping once the design is locked.
  const [sourceVariant, setSourceVariant] = useState(VARIANT_SINGLE_OUTLINE)
  const switchConnectionMethod = useConfigureRoleStore(
    (s) => s.switchConnectionMethod,
  )
  // CLJS shows the picker on every credentials tab — see
  // server.cljs:43, server.cljs:137, server.cljs:186, network.cljs:34/84,
  // metadata_driven.cljs:121-139, claude_code_edit.cljs:59.
  const awsIamAvailable = supportsAwsIam(connection.subtype)
  const isSecretsManager = selectedMethod === CONNECTION_METHODS.SECRETS_MANAGER
  const isDerivedMethod = selectedMethod === derivedMethod

  // Switching method is a fresh start in write-only land: existing
  // "Set" cards become meaningless (the user can't peek at the value
  // they would re-encode), so we clear all fields and let the user
  // re-enter. The store also stages deletes for any persisted
  // provider reference that doesn't belong to the new method so the
  // save patch actually wipes them on the wire — otherwise the next
  // load would re-derive the old method from the surviving refs.
  const setSelectedMethod = (next) => {
    if (next === selectedMethod) return
    switchConnectionMethod(next)
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
    <SourcedInputVariantProvider value={sourceVariant}>
      <Stack gap="xl" maw={720}>
        {/* TEMPORARY: variant preview switch. Remove after the design
            decision lands. */}
        <Group gap="sm" align="center">
          <Text size="xs" c="dimmed">
            Source picker variant (preview):
          </Text>
          <Switch
            size="sm"
            checked={sourceVariant === VARIANT_GLUED_SIBLINGS}
            onChange={(e) =>
              setSourceVariant(
                e.currentTarget.checked
                  ? VARIANT_GLUED_SIBLINGS
                  : VARIANT_SINGLE_OUTLINE,
              )
            }
            label={
              sourceVariant === VARIANT_GLUED_SIBLINGS
                ? 'Glued siblings'
                : 'Single outline'
            }
          />
        </Group>
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
    </SourcedInputVariantProvider>
  )
}
