import { Stack } from '@mantine/core'
import EnvironmentVariablesSection from './shared/EnvironmentVariablesSection'
import ConfigurationFilesSection from './shared/ConfigurationFilesSection'
import CommandArgsSection from './shared/CommandArgsSection'
import AgentSelectorSection from './shared/AgentSelectorSection'

// Free-form custom connection editor. Matches the CLJS
// server/credentials-step layout: env vars list + configuration files
// list + additional command arguments + agent picker.
//
// Used by custom connections without a catalog entry — i.e.
// custom/(empty subtype) and custom/linux-vm — and by every custom
// subtype that the catalog doesn't define (legacy fallback).
export default function FreeFormCustomRenderer({ connection, availableSources }) {
  return (
    <Stack gap="xl">
      <EnvironmentVariablesSection
        connection={connection}
        availableSources={availableSources}
      />
      <ConfigurationFilesSection connection={connection} />
      <CommandArgsSection />
      <AgentSelectorSection />
    </Stack>
  )
}
