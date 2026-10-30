import { useMemo } from 'react'
import { Anchor, Group, Stack, Text, Title } from '@mantine/core'
import { useStore } from 'zustand'
import { ExternalLink, Trash2 } from 'lucide-react'
import AttributesSelect from '@/components/AttributesSelect'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import { labelForManagedAttribute } from '@/features/ProtectionProfiles/constants'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { CONNECTION_METHODS, supportsAwsIam } from '@/utils/connectionPolicy'
import { jsonCredentialToField } from '@/utils/connectionsMetadataMapper'
import { docsUrl } from '@/utils/docsUrl'
import { SOURCES } from '@/pages/Roles/Configure/utils/secretsCodec'
import {
  ConnectionMethodSection,
  SecretsManagerProviderSection,
} from '@/pages/Roles/Configure/components/ConnectionMethod'
import { CredentialsStoreProvider } from '@/pages/Roles/Configure/sections/credentials/store'
import FreeFormCustomRenderer from '@/pages/Roles/Configure/sections/credentials/FreeFormCustomRenderer'
import HttpProxyRenderer from '@/pages/Roles/Configure/sections/credentials/HttpProxyRenderer'
import SshRenderer from '@/pages/Roles/Configure/sections/credentials/SshRenderer'
import PredefinedFields from '@/pages/Roles/Configure/sections/credentials/shared/PredefinedFields'
import { metadataForSubtype, roleFormFor } from '../catalog'
import StepRow from '../components/StepRow'
import { useResourceSetupStore } from '../store'

// The CLJS wizard's label: HOST → Host, PRE_SQL_SCRIPT_FILE → Pre sql script file.
function wizardLabel(name) {
  const words = name.replace(/[^a-zA-Z0-9]/g, ' ').toLowerCase()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

function RoleAttributes({ role }) {
  const attributes = useResourceSetupStore((s) => s.attributes)
  const profile = useResourceSetupStore((s) => s.profile)
  const setRoleAttributes = useResourceSetupStore((s) => s.setRoleAttributes)
  const createAttribute = useResourceSetupStore((s) => s.createAttribute)

  // While a protection profile is active its attribute is pre-selected; removing
  // the pill opts this role out of the profile.
  const profileAttribute = profile?.profile && profile?.attribute_name ? profile.attribute_name : null
  const options = attributes
    .filter((a) => !a.managed_by)
    .map((a) => ({ value: a.name, label: a.name }))
  if (profileAttribute) {
    options.unshift({
      value: profileAttribute,
      label: labelForManagedAttribute(profileAttribute),
      managed: true,
    })
  }
  const value = profileAttribute && !role.skipProfile
    ? [profileAttribute, ...role.attributes]
    : role.attributes

  return (
    <Stack gap="xs">
      <AttributesSelect
        label="Attributes"
        options={options}
        value={value}
        placeholder="Select or type to create"
        onChange={(next) =>
          setRoleAttributes(
            role.id,
            next.filter((v) => v !== profileAttribute),
            profileAttribute ? !next.includes(profileAttribute) : role.skipProfile,
          )
        }
        onCreate={(name) => createAttribute(role.id, name)}
      />
      <Text size="sm" c="dimmed">
        Determine how protection rules and access policies apply to this role.
      </Text>
    </Stack>
  )
}

function CredentialsForm({ form, role, fields, connection, availableSources }) {
  const props = { connection, availableSources, forceNewState: false, hideRoleInfo: false }
  if (form === 'ssh') return <SshRenderer {...props} />
  if (form === 'httpproxy') return <HttpProxyRenderer {...props} />
  if (form === 'linux-vm') return <FreeFormCustomRenderer {...props} />
  return <PredefinedFields {...props} fields={fields} connectionMethod={role.method} />
}

function RoleCredentials({ form, role, fields }) {
  const setRoleMethod = useResourceSetupStore((s) => s.setRoleMethod)
  const setRoleProvider = useResourceSetupStore((s) => s.setRoleProvider)
  const connection = useStore(role.credentials, (s) => s.connection)
  // Local SSH has no credentials, so no method.
  const localSsh = useStore(role.credentials, (s) => s.drafts.subtype === 'ssh-local')
  const isSecretsManager = role.method === CONNECTION_METHODS.SECRETS_MANAGER

  return (
    <Stack gap="xl">
      {!localSsh && (
        <Stack gap="md">
          <ConnectionMethodSection
            heading="Role connection method"
            selectedMethod={role.method}
            onSelect={(method) => setRoleMethod(role.id, method)}
            awsIamAvailable={supportsAwsIam(connection.subtype)}
            federationAvailable={false}
          />
          {isSecretsManager && (
            <SecretsManagerProviderSection
              provider={role.provider}
              onProviderChange={(provider) => setRoleProvider(role.id, provider)}
            />
          )}
          {role.method === CONNECTION_METHODS.AWS_IAM && (
            <Anchor
              size="sm"
              href={docsUrl.setup.configuration.rdsIamAuth}
              target="_blank"
              rel="noopener noreferrer"
              w="fit-content"
            >
              <Group gap={4} component="span" wrap="nowrap">
                <ExternalLink size={12} />
                Learn more about AWS IAM Role setup
              </Group>
            </Anchor>
          )}
        </Stack>
      )}
      <CredentialsStoreProvider value={role.credentials}>
        <CredentialsForm
          form={form}
          role={role}
          fields={fields}
          connection={connection}
          availableSources={isSecretsManager ? [role.provider, SOURCES.MANUAL] : null}
        />
      </CredentialsStoreProvider>
    </Stack>
  )
}

export default function RoleForm({ role }) {
  const setup = useResourceSetupStore((s) => s.setup)
  const canRemove = useResourceSetupStore((s) => s.roles.length > 1)
  const setRoleName = useResourceSetupStore((s) => s.setRoleName)
  const removeRole = useResourceSetupStore((s) => s.removeRole)
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  const form = roleFormFor(setup.subtype)
  // Dev envs have no credentials: no method, no fields.
  const fields = useMemo(() => {
    const credentials = metadataForSubtype(metadata?.connections, setup.subtype)
      ?.resourceConfiguration?.credentials
    if (!credentials?.length) return null
    return credentials.map((entry) => ({ ...jsonCredentialToField(entry), label: wizardLabel(entry.name) }))
  }, [metadata, setup.subtype])

  return (
    <StepRow
      title="New Role"
      description="Fill out the information to access your Resource with this specific Role."
    >
      <Stack gap="xxlAlt">
        <Stack gap="md">
          <Title order={4}>Role information</Title>
          <TextInput
            label="Name"
            placeholder="e.g. read-only"
            value={role.name}
            required
            onChange={(e) => setRoleName(role.id, e.currentTarget.value)}
          />
          <RoleAttributes role={role} />
        </Stack>

        {(form !== 'metadata' || fields) && <RoleCredentials form={form} role={role} fields={fields} />}

        {canRemove && (
          <Group justify="flex-end">
            <Button
              size="sm"
              variant="subtle"
              color="red"
              leftSection={<Trash2 size={16} />}
              onClick={() => removeRole(role.id)}
            >
              Remove Role
            </Button>
          </Group>
        )}
      </Stack>
    </StepRow>
  )
}
