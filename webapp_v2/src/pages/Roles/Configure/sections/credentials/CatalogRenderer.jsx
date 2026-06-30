import { Stack, Title } from '@mantine/core'
import PredefinedFields from './shared/PredefinedFields'
import AgentSelectorSection from './shared/AgentSelectorSection'

// Renders any catalog-driven connection: databases (postgres, mysql,
// …), catalog applications (git, github, tcp), and catalog custom
// subtypes (dynamodb, aws-*, kubernetes, redis, …). Fields come from
// connections-metadata.json via the metadata store; values stay
// write-only (the backend strips them on read), so each field renders
// in the Set → Replace pattern.
export default function CatalogRenderer({
  connection,
  fields,
  availableSources,
  forceNewState,
  connectionMethod,
  hideRoleInfo,
}) {
  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={4}>Environment credentials</Title>
        <PredefinedFields
          connection={connection}
          fields={fields}
          availableSources={availableSources}
          forceNewState={forceNewState}
          connectionMethod={connectionMethod}
          hideRoleInfo={hideRoleInfo}
        />
      </Stack>
      <AgentSelectorSection />
    </Stack>
  )
}
