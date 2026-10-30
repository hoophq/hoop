import { Anchor, Group, Stack, Title } from '@mantine/core'
import { FileSpreadsheet, GlobeLock, ExternalLink, Fingerprint } from 'lucide-react'
import SelectionCard from '@/components/SelectionCard'
import Select from '@/components/Select'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'
import { SOURCES, SOURCE_LABELS } from '@/pages/Roles/Configure/utils/secretsCodec'

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
    icon: FileSpreadsheet,
  },
  {
    id: CONNECTION_METHODS.SECRETS_MANAGER,
    title: 'Secrets Manager',
    description:
      'Connect to a secrets provider like AWS Secrets Manager or HashiCorp Vault to automatically fetch your resource credentials.',
    icon: GlobeLock,
  },
  {
    id: CONNECTION_METHODS.AWS_IAM,
    title: 'AWS IAM Role',
    description:
      'Use an IAM Role that can be assumed to authenticate and access AWS resources.',
    image: '/icons/automatic-resources/aws.svg',
  },
  {
    id: CONNECTION_METHODS.IAM_FEDERATION,
    title: 'IAM Federation',
    description:
      'Impersonate users at session time via GCP IAM — no static credentials stored per user.',
    icon: Fingerprint,
  },
]

export function ConnectionMethodSection({
  selectedMethod,
  onSelect,
  awsIamAvailable,
  federationAvailable,
  heading = 'Connection method',
}) {
  const visibleMethods = METHOD_DEFINITIONS.filter((m) => {
    if (m.id === CONNECTION_METHODS.AWS_IAM) return awsIamAvailable
    if (m.id === CONNECTION_METHODS.IAM_FEDERATION) return federationAvailable
    return true
  })
  return (
    <Stack gap="md">
      <Title order={4}>{heading}</Title>
      <Stack gap="sm">
        {visibleMethods.map(({ id, title, description, icon, image }) => (
          <SelectionCard
            key={id}
            icon={icon}
            image={image}
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

export function SecretsManagerProviderSection({ provider, onProviderChange }) {
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
        w="fit-content"
      >
        <Group gap={4} component="span" wrap="nowrap">
          <ExternalLink size={12} />
          Learn more about secrets manager setup
        </Group>
      </Anchor>
    </Stack>
  )
}
