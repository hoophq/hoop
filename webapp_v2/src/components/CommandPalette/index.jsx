import { useState, useEffect } from 'react'
import { SpotlightRoot, SpotlightSearch, SpotlightActionsList, spotlight } from '@mantine/spotlight'
import { useDebouncedValue } from '@mantine/hooks'
import { Search } from 'lucide-react'
import { useCommandPaletteStore } from '@/stores/useCommandPaletteStore'
import { searchAll } from '@/services/search'
import MainPage from './pages/MainPage'
import ResourceRolesPage from './pages/ResourceRolesPage'
import ConnectionActionsPage from './pages/ConnectionActionsPage'

export { spotlight }

export const openCommandPalette = () => spotlight.open()

function CommandPalette() {
  const [query, setQuery] = useState('')
  const [debouncedQuery] = useDebouncedValue(query, 300)
  const { currentPage, setSearchResults, reset } = useCommandPaletteStore()

  useEffect(() => {
    if (debouncedQuery.length >= 2) {
      setSearchResults('searching', {})
      searchAll(debouncedQuery)
        .then((r) => setSearchResults('ready', r.data))
        .catch(() => setSearchResults('error', {}))
    } else {
      setSearchResults('idle', {})
    }
  }, [debouncedQuery])

  const handleKeyDown = (event) => {
    if (event.key === 'Backspace' && query === '' && currentPage !== 'main') {
      useCommandPaletteStore.getState().back()
    }
  }

  const handleClose = () => {
    setQuery('')
    reset()
  }

  return (
    <SpotlightRoot
      query={query}
      onQueryChange={setQuery}
      onSpotlightClose={handleClose}
      shortcut={['mod + K']}
      scrollable
      maxHeight={400}
      clearQueryOnClose={false}
    >
      <SpotlightSearch
        leftSection={<Search size={16} />}
        placeholder="Search for resources, connections, runbooks..."
        onKeyDown={handleKeyDown}
      />
      <SpotlightActionsList>
        {currentPage === 'main' && <MainPage query={debouncedQuery} />}
        {currentPage === 'resource-roles' && <ResourceRolesPage />}
        {currentPage === 'connection-actions' && <ConnectionActionsPage />}
      </SpotlightActionsList>
    </SpotlightRoot>
  )
}

export default CommandPalette
