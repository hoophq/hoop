import { SpotlightAction, SpotlightActionsGroup, SpotlightEmpty } from '@mantine/spotlight'
import { Loader, Text, Group } from '@mantine/core'
import { Package, Rotate3d, File, ChevronRight } from 'lucide-react'
import { SUGGESTION_ITEMS, QUICK_ACCESS_ITEMS } from './constants'

function SuggestionsAndQuickAccess({ onNavigate }) {
  return (
    <>
      <SpotlightActionsGroup label="Suggestions">
        {SUGGESTION_ITEMS.map((item) => (
          <SpotlightAction
            key={item.id}
            label={item.label}
            leftSection={<item.icon size={16} />}
            onClick={() => onNavigate(item.path)}
          />
        ))}
      </SpotlightActionsGroup>
      <SpotlightActionsGroup label="Quick Access">
        {QUICK_ACCESS_ITEMS.map((item) => (
          <SpotlightAction
            key={item.id}
            label={item.label}
            leftSection={<item.icon size={16} />}
            onClick={() => onNavigate(item.path)}
          />
        ))}
      </SpotlightActionsGroup>
    </>
  )
}

export default function MainPage({
  query,
  searchStatus,
  searchResults,
  onResourceSelect,
  onConnectionSelect,
  onRunbookSelect,
  onNavigate
}) {
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
      return (
        <>
          <SpotlightEmpty>No results found for "{query}"</SpotlightEmpty>
          <SuggestionsAndQuickAccess onNavigate={onNavigate} />
        </>
      )
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
                rightSection={<ChevronRight size={16} />}
                closeSpotlightOnTrigger={false}
                onClick={() => onResourceSelect(r)}
              />
            ))}
          </SpotlightActionsGroup>
        )}
        {connections.length > 0 && (
          <SpotlightActionsGroup label="Roles">
            {connections.map((c) => (
              <SpotlightAction
                key={c.name}
                label={c.name}
                description={c.resource_name}
                leftSection={<Rotate3d size={16} />}
                rightSection={<ChevronRight size={16} />}
                closeSpotlightOnTrigger={false}
                onClick={() => onConnectionSelect(c)}
              />
            ))}
          </SpotlightActionsGroup>
        )}
        {runbooks.length > 0 && (
          <SpotlightActionsGroup label="Runbooks">
            {runbooks.map((rb) => {
              const repoName = rb.repository ? rb.repository.split('/').pop() : ''
              const nameParts = rb.name.split('/')
              const folder = nameParts.length > 1 ? nameParts.slice(0, -1).join('/') + '/' : ''
              const filename = nameParts[nameParts.length - 1]

              return (
                <SpotlightAction
                  key={`${rb.repository}:${rb.name}`}
                  label={
                    <Group gap="xs">
                      {repoName && <Text component="span" size="sm" c="dimmed">@{repoName}</Text>}
                      {folder && <Text component="span" size="sm" c="dimmed">{folder}</Text>}
                      <Text component="span" size="sm">{filename}</Text>
                    </Group>
                  }
                  leftSection={<File size={16} />}
                  onClick={() => onRunbookSelect(rb)}
                />
              )
            })}
          </SpotlightActionsGroup>
        )}
        <SuggestionsAndQuickAccess onNavigate={onNavigate} />
      </>
    )
  }

  return <SuggestionsAndQuickAccess onNavigate={onNavigate} />
}
