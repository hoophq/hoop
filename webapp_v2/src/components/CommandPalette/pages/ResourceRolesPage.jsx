import { SpotlightAction, SpotlightActionsGroup, SpotlightEmpty } from '@mantine/spotlight'
import { Plug, ArrowLeft } from 'lucide-react'
import { useCommandPaletteStore } from '@/stores/useCommandPaletteStore'

export default function ResourceRolesPage() {
  const { context, searchResults, navigateToPage, back } = useCommandPaletteStore()
  const resource = context.resource

  const connections = (searchResults.connections || []).filter(
    (c) => c.resource_name === resource?.name
  )

  return (
    <SpotlightActionsGroup label={`Connections for ${resource?.name || 'resource'}`}>
      <SpotlightAction
        label="← Back"
        leftSection={<ArrowLeft size={16} />}
        closeSpotlightOnTrigger={false}
        onClick={back}
      />
      {connections.length === 0
        ? <SpotlightEmpty>No connections found for this resource.</SpotlightEmpty>
        : connections.map((c) => (
          <SpotlightAction
            key={c.name}
            label={c.name}
            description={c.type || c.subtype}
            leftSection={<Plug size={16} />}
            closeSpotlightOnTrigger={false}
            onClick={() => navigateToPage('connection-actions', { connection: c, resource })}
          />
        ))
      }
    </SpotlightActionsGroup>
  )
}
