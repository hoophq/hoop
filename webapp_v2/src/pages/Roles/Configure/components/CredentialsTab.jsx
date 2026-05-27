import { useState } from 'react'
import { Stack, Title, Text, Alert } from '@mantine/core'
import { FileText, Cloud, ShieldCheck, Info, TriangleAlert } from 'lucide-react'
import SelectionCard from '@/components/SelectionCard'
import PredefinedFieldsCredentials from './PredefinedFieldsCredentials'
import SSHCredentials from './SSHCredentials'
import CustomCredentials from './CustomCredentials'
import {
  CATALOG_FIELDS,
  CONNECTION_METHODS,
  supportsConnectionMethods,
} from '../utils/credentialsSchema'
import { deriveConnectionMethod } from '../utils/connectionMethod'

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

function CredentialsBody({ connection, isAdmin }) {
  const { type, subtype } = connection

  // Mirrors the dispatch in CLJS credentials_tab.cljs verbatim.
  if (type === 'database' && CATALOG_FIELDS[subtype]) {
    return (
      <Stack gap="md">
        <Title order={4}>Environment credentials</Title>
        <PredefinedFieldsCredentials
          connection={connection}
          fields={CATALOG_FIELDS[subtype]}
          isAdmin={isAdmin}
        />
      </Stack>
    )
  }

  if (type === 'application' && (subtype === 'ssh' || subtype === 'git' || subtype === 'github')) {
    return <SSHCredentials connection={connection} isAdmin={isAdmin} />
  }

  if (type === 'httpproxy' && subtype === 'claude-code') {
    return (
      <Stack gap="md">
        <Title order={4}>Basic info</Title>
        <PredefinedFieldsCredentials
          connection={connection}
          fields={CATALOG_FIELDS['claude-code']}
          isAdmin={isAdmin}
        />
      </Stack>
    )
  }

  if (type === 'httpproxy') {
    return (
      <Stack gap="md">
        <Title order={4}>Environment credentials</Title>
        <PredefinedFieldsCredentials
          connection={connection}
          fields={CATALOG_FIELDS.httpproxy}
          isAdmin={isAdmin}
        />
      </Stack>
    )
  }

  if (type === 'custom' && subtype === 'kubernetes-token') {
    return (
      <Stack gap="md">
        <Title order={4}>Kubernetes token</Title>
        <PredefinedFieldsCredentials
          connection={connection}
          fields={CATALOG_FIELDS['kubernetes-token']}
          isAdmin={isAdmin}
        />
      </Stack>
    )
  }

  if (type === 'custom' && CATALOG_FIELDS[subtype]) {
    return (
      <Stack gap="md">
        <Title order={4}>Environment credentials</Title>
        <PredefinedFieldsCredentials
          connection={connection}
          fields={CATALOG_FIELDS[subtype]}
          isAdmin={isAdmin}
        />
      </Stack>
    )
  }

  if (type === 'custom') {
    return <CustomCredentials connection={connection} isAdmin={isAdmin} />
  }

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

export default function CredentialsTab({ connection, isAdmin }) {
  const derivedMethod = deriveConnectionMethod(connection.secret)
  const [selectedMethod, setSelectedMethod] = useState(derivedMethod)
  const showMethodCards = supportsConnectionMethods(connection)
  const awsIamAvailable = supportsAwsIam(connection.subtype)
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
      <CredentialsBody connection={connection} isAdmin={isAdmin} />
    </Stack>
  )
}
