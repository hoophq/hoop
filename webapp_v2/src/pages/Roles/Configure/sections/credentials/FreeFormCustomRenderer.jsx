import { Stack } from '@mantine/core'
import EnvironmentVariables from './shared/EnvironmentVariables'
import ConfigurationFiles from './shared/ConfigurationFiles'
import CommandArgs from './shared/CommandArgs'
import AgentSelector from './shared/AgentSelector'

// Free-form custom connection editor. Matches the CLJS
// server/credentials-step layout: env vars list + configuration files
// list + additional command arguments + agent picker.
//
// Used by custom connections without a catalog entry — i.e.
// custom/(empty subtype) and custom/linux-vm — and by every custom
// subtype that the catalog doesn't define (legacy fallback).
export default function FreeFormCustomRenderer({ connection, availableSources, hideRoleInfo }) {
  return (
    <Stack gap="xl">
      <EnvironmentVariables
        connection={connection}
        availableSources={availableSources}
        hideRoleInfo={hideRoleInfo}
      />
      <ConfigurationFiles connection={connection} hideRoleInfo={hideRoleInfo} />
      <CommandArgs />
      <AgentSelector />
    </Stack>
  )
}
