import { useState } from 'react'
import { Stack, Text } from '@mantine/core'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import PageLoader from '@/components/PageLoader'
import {
  CONNECTION_METHODS,
  supportsAwsIam,
  supportsIamFederation,
} from '@/utils/connectionPolicy'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { deriveConnectionInfo } from '@/pages/Roles/Configure/utils/connectionMethod'
import { SOURCES } from '@/pages/Roles/Configure/utils/secretsCodec'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'
import { pickRendererRule } from '@/pages/Roles/Configure/sections/credentials'
import { formatTimestamp } from '@/pages/Roles/Configure/components/SecretField/util'
import FederationSection from '@/pages/Roles/Configure/sections/FederationSection'
import {
  ConnectionMethodSection,
  SecretsManagerProviderSection,
} from '@/pages/Roles/Configure/components/ConnectionMethod'

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

function CredentialsBody({ connection, availableSources, forceNewState, connectionMethod, hideRoleInfo }) {
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
    { connection, availableSources, forceNewState, connectionMethod, hideRoleInfo },
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
  const lastUpdated = formatTimestamp(connection.secrets_updated_at)
  return (
    <Stack gap="md">
      {lastUpdated && (
        <Text size="xs" c="dimmed">{'Last updated ' + lastUpdated}</Text>
      )}
      {node}
    </Stack>
  )
}

export default function CredentialsTab({ connection }) {
  const federationStatus = useConfigureRoleStore((s) => s.federation.status)
  const federationAvailable = supportsIamFederation(connection.subtype)
  if (federationAvailable && (federationStatus === 'idle' || federationStatus === 'loading')) {
    return <PageLoader h={200} />
  }
  return <CredentialsEditor connection={connection} federationAvailable={federationAvailable} />
}

function CredentialsEditor({ connection, federationAvailable }) {
  // Derive method + provider from existing values so the top-level
  // controls reflect what's actually stored, not a hardcoded default.
  const derived = deriveConnectionInfo(connection.secret)
  const federationConfigured = useConfigureRoleStore((s) => Boolean(s.federation.config))
  const federationError = useConfigureRoleStore((s) => s.federation.error)
  const selectFederation = useConfigureRoleStore((s) => s.selectFederation)
  const [selectedMethod, setSelectedMethodState] = useState(
    () => deriveConnectionInfo(connection.secret, { federated: federationConfigured }).method,
  )
  const [secretsProvider, setSecretsProviderState] = useState(
    derived.provider || SOURCES.AWS_SECRETS_MANAGER,
  )
  const switchConnectionMethod = useConfigureRoleStore(
    (s) => s.switchConnectionMethod,
  )
  const setSecretsManagerProvider = useConfigureRoleStore(
    (s) => s.setSecretsManagerProvider,
  )
  const hideRoleInfo = useConfigureRoleStore((s) => s.hideRoleInfo)
  // Local SSH runs on the agent host — no credentials, so the
  // credential-source chooser is meaningless. Follows the draft (not
  // the persisted subtype) so the section hides/shows as the user flips
  // the Connection Type radio in SshRenderer. Mirrors CLJS server.cljs,
  // which renders connection-method/main only in proxy mode.
  const draftSubtype = useConfigureRoleStore((s) => s.drafts.subtype)
  const isLocalSsh =
    connection.type === 'application' && draftSubtype === 'ssh-local'
  const awsIamAvailable = supportsAwsIam(connection.subtype)
  const isSecretsManager = selectedMethod === CONNECTION_METHODS.SECRETS_MANAGER
  const isFederation = selectedMethod === CONNECTION_METHODS.IAM_FEDERATION
  const isDerivedMethod = selectedMethod === derived.method

  // Method switch wipes existing fields and stages deletes for any
  // surviving reference that doesn't belong to the new method.
  const setSelectedMethod = (next) => {
    if (next === selectedMethod) return
    switchConnectionMethod(next)
    selectFederation(next === CONNECTION_METHODS.IAM_FEDERATION)
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
      {federationError && (
        <CredentialsError
          title="Could not load the IAM federation of this role."
          message={federationError}
        />
      )}
      {!isLocalSsh && (
        <ConnectionMethodSection
          selectedMethod={selectedMethod}
          onSelect={setSelectedMethod}
          awsIamAvailable={awsIamAvailable}
          federationAvailable={federationAvailable}
        />
      )}
      {!isLocalSsh && isSecretsManager && (
        <SecretsManagerProviderSection
          provider={secretsProvider}
          onProviderChange={setSecretsProvider}
        />
      )}
      {isFederation ? (
        <FederationSection />
      ) : (
        <CredentialsBody
          connection={connection}
          availableSources={availableSources}
          forceNewState={forceNewState}
          connectionMethod={selectedMethod}
          hideRoleInfo={hideRoleInfo}
        />
      )}
    </Stack>
  )
}
