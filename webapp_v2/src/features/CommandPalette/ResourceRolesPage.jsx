import { SpotlightAction, SpotlightActionsGroup, SpotlightEmpty } from '@mantine/spotlight'
import { Rotate3d, ChevronRight } from 'lucide-react'

export default function ResourceRolesPage({ resource, connections, onSelect }) {
  return (
    <SpotlightActionsGroup label={`Connections for ${resource?.name || 'resource'}`}>
      {connections.length === 0
        ? <SpotlightEmpty>No connections found for this resource.</SpotlightEmpty>
        : connections.map((c) => (
          <SpotlightAction
            key={c.name}
            label={c.name}
            description={c.type || c.subtype}
            leftSection={<Rotate3d size={16} />}
            rightSection={<ChevronRight size={16} />}
            closeSpotlightOnTrigger={false}
            onClick={() => onSelect(c)}
          />
        ))
      }
    </SpotlightActionsGroup>
  )
}
