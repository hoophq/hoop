import { SpotlightAction, SpotlightActionsGroup, SpotlightEmpty } from '@mantine/spotlight'
import { Loader, Text } from '@mantine/core'
import { useNavigate } from 'react-router-dom'
import { spotlight } from '@mantine/spotlight'
import { Package, Rotate3d, File } from 'lucide-react'
import { useCommandPaletteStore } from '@/stores/useCommandPaletteStore'
import { NAVIGATION_ACTIONS } from '../constants'

function groupBy(arr, key) {
  return arr.reduce((acc, item) => {
    const k = item[key]
    if (!acc[k]) acc[k] = []
    acc[k].push(item)
    return acc
  }, {})
}

export default function MainPage({ query, navigate: _navigate }) {
  const navigate = useNavigate()
  const { searchStatus, searchResults, navigateToPage } = useCommandPaletteStore()

  const hasQuery = query.trim().length >= 2

  if (hasQuery) {
    if (searchStatus === 'searching') {
      return (
        <SpotlightEmpty>
          <Loader size="sm" />
          <Text size="sm" c="dimmed" ml="xs">Searching...</Text>
        </SpotlightEmpty>
      )
    }

    if (searchStatus === 'error') {
      return <SpotlightEmpty>Search failed. Try again.</SpotlightEmpty>
    }

    const { resources = [], connections = [], runbooks = [] } = searchResults
    const hasResults = resources.length > 0 || connections.length > 0 || runbooks.length > 0

    if (searchStatus === 'ready' && !hasResults) {
      return <SpotlightEmpty>No results found for "{query}"</SpotlightEmpty>
    }

    return (
      <>
        {resources.length > 0 && (
          <SpotlightActionsGroup label="Resources">
            {resources.map((r) => (
              <SpotlightAction
                key={r.name}
                label={r.name}
                description={r.type || r.subtype}
                leftSection={<Package size={16} />}
                closeSpotlightOnTrigger={false}
                onClick={() => navigateToPage('resource-roles', { resource: r })}
              />
            ))}
          </SpotlightActionsGroup>
        )}
        {connections.length > 0 && (
          <SpotlightActionsGroup label="Connections">
            {connections.map((c) => (
              <SpotlightAction
                key={c.name}
                label={c.name}
                description={c.resource_name}
                leftSection={<Rotate3d size={16} />}
                closeSpotlightOnTrigger={false}
                onClick={() => navigateToPage('connection-actions', { connection: c })}
              />
            ))}
          </SpotlightActionsGroup>
        )}
        {runbooks.length > 0 && (
          <SpotlightActionsGroup label="Runbooks">
            {runbooks.map((rb) => (
              <SpotlightAction
                key={rb.name}
                label={rb.name}
                description={rb.description}
                leftSection={<File size={16} />}
                onClick={() => {
                  spotlight.close()
                  navigate('/runbooks')
                }}
              />
            ))}
          </SpotlightActionsGroup>
        )}
      </>
    )
  }

  // No query — show Quick Access (navigation items)
  const grouped = groupBy(NAVIGATION_ACTIONS, 'group')

  return (
    <>
      {Object.entries(grouped).map(([group, items]) => (
        <SpotlightActionsGroup key={group} label={group}>
          {items.map((item) => (
            <SpotlightAction
              key={item.id}
              label={item.label}
              description={item.description}
              leftSection={<item.icon size={16} />}
              onClick={() => {
                spotlight.close()
                navigate(item.path)
              }}
            />
          ))}
        </SpotlightActionsGroup>
      ))}
    </>
  )
}
