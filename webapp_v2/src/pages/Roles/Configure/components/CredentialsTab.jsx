import { useState } from 'react'
import { Stack, Title, Text, Alert } from '@mantine/core'
import { FileText, Cloud, ShieldCheck, Info, TriangleAlert } from 'lucide-react'
import SelectionCard from '@/components/SelectionCard'
import SecretField from './SecretField'
import {
  CATALOG_FIELDS,
  CONNECTION_METHODS,
  isCatalogSubtype,
  supportsConnectionMethods,
} from '../utils/credentialsSchema'
import {
  decodeSecretValue,
  encodeSecretValue,
  isSecretReference,
} from '../utils/secretsCodec'
import { deriveConnectionMethod } from '../utils/connectionMethod'
import { useConfigureRoleStore } from '../store'

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

// Whether the AWS IAM Role card should be offered. CLJS limits it to
// MySQL and Postgres because those are the only RDS auth backends the
// gateway/agent currently support.
function supportsAwsIam(subtype) {
  return subtype === 'postgres' || subtype === 'mysql'
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

function MethodSwitchNotice({ derivedMethod, selectedMethod }) {
  const labelOf = (id) =>
    METHOD_DEFINITIONS.find((m) => m.id === id)?.title || id
  return (
    <Alert variant="light" color="yellow" icon={<TriangleAlert size={16} />}>
      <Stack gap={4}>
        <Text size="sm" fw={600}>
          {'Switching from ' + labelOf(derivedMethod) + ' to ' + labelOf(selectedMethod) + ' requires re-entering all credentials.'}
        </Text>
        <Text size="sm">
          This is not yet supported by the new editor. Save the current
          connection unchanged, or use the legacy editor to switch methods.
        </Text>
      </Stack>
    </Alert>
  )
}

function CatalogCredentials({ connection, isAdmin }) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)

  const subtype = connection.subtype
  const fields = CATALOG_FIELDS[subtype] || []
  const currentSecrets = connection.secret || {}

  return (
    <Stack gap="md">
      <Title order={4}>Environment credentials</Title>
      <Stack gap="lg">
        {fields.map((field) => {
          const envKey = `envvar:${field.key.toUpperCase()}`
          const encodedValue = currentSecrets[envKey]
          const isExisting =
            envKey in currentSecrets &&
            (encodedValue !== '' || connection.secrets_updated_at != null)
          const isReference = isSecretReference(encodedValue)
          const referenceText = isReference ? decodeSecretValue(encodedValue) : ''
          const staged = stagedSecrets[envKey]
          return (
            <SecretField
              key={envKey}
              label={field.label}
              required={field.required}
              placeholder={field.placeholder}
              type={field.type}
              isExisting={isExisting}
              isReference={isReference}
              referenceText={referenceText}
              allowDelete={false}
              stagedAction={staged?.action}
              stagedValue={
                staged?.value ? decodeSecretValue(staged.value) : ''
              }
              secretsUpdatedAt={connection.secrets_updated_at}
              onReplace={(plain) =>
                isAdmin && replaceSecret(envKey, encodeSecretValue(plain))
              }
              onChangeStaged={(plain) =>
                isAdmin && replaceSecret(envKey, encodeSecretValue(plain))
              }
              onCancel={() => cancelSecretChange(envKey)}
            />
          )
        })}
      </Stack>
    </Stack>
  )
}

function UnsupportedCredentialsPlaceholder({ connection }) {
  return (
    <Alert variant="light" color="yellow" icon={<Info size={16} />}>
      <Stack gap={4}>
        <Text size="sm" fw={600}>
          {'Editing credentials for ' +
            (connection.subtype || connection.type) +
            ' connections is not yet available in the new editor.'}
        </Text>
        <Text size="sm">
          This page is mid-migration; use the legacy form to edit non-catalog
          credentials. The write-only treatment still applies — values are
          never returned by the API.
        </Text>
      </Stack>
    </Alert>
  )
}

export default function CredentialsTab({ connection, isAdmin }) {
  const derivedMethod = deriveConnectionMethod(connection.secret)
  const [selectedMethod, setSelectedMethod] = useState(derivedMethod)
  const showMethodCards = supportsConnectionMethods(connection)
  const awsIamAvailable = supportsAwsIam(connection.subtype)
  const supported = isCatalogSubtype(connection.subtype) && connection.type === 'database'
  const methodMismatch = selectedMethod !== derivedMethod

  return (
    <Stack gap="xxl" maw={720}>
      <WriteOnlyNotice />
      {showMethodCards && (
        <ConnectionMethodSection
          selectedMethod={selectedMethod}
          onSelect={setSelectedMethod}
          awsIamAvailable={awsIamAvailable}
        />
      )}
      {methodMismatch && (
        <MethodSwitchNotice
          derivedMethod={derivedMethod}
          selectedMethod={selectedMethod}
        />
      )}
      {supported ? (
        <CatalogCredentials connection={connection} isAdmin={isAdmin} />
      ) : (
        <UnsupportedCredentialsPlaceholder connection={connection} />
      )}
    </Stack>
  )
}
