import { useState, useEffect } from 'react';
import { SpotlightRoot, SpotlightSearch, SpotlightActionsList, spotlight } from '@mantine/spotlight';
import { useDebouncedValue } from '@mantine/hooks';
import { Badge, ActionIcon } from '@mantine/core';
import { Search, X } from 'lucide-react';
import { useCommandPaletteStore } from '@/stores/useCommandPaletteStore';
import { searchAll } from '@/services/search';
import MainPage from './pages/MainPage';
import ResourceRolesPage from './pages/ResourceRolesPage';
import ConnectionActionsPage from './pages/ConnectionActionsPage';

export { spotlight };

export const openCommandPalette = () => spotlight.open();

function CommandPalette() {
  const [query, setQuery] = useState('');
  const [debouncedQuery] = useDebouncedValue(query, 300);
  const { currentPage, context, setSearchResults, reset, back } = useCommandPaletteStore();

  useEffect(
    () => {
      if (debouncedQuery.length >= 2) {
        setSearchResults('searching', {});
        searchAll(debouncedQuery)
          .then(r => setSearchResults('ready', r.data))
          .catch(() => setSearchResults('error', {}));
      } else {
        setSearchResults('idle', {});
      }
    },
    [debouncedQuery]
  );

  const handleKeyDown = event => {
    if (event.key === 'Backspace' && query === '' && currentPage !== 'main') {
      back();
    }
  };

  const handleClose = () => {
    setQuery('');
    reset();
  };

  const badgeLabel =
    currentPage === 'resource-roles'
      ? context.resource && context.resource.name
      : currentPage === 'connection-actions' ? context.connection && context.connection.name : null;

  const placeholder =
    currentPage === 'resource-roles'
      ? 'Select a connection...'
      : currentPage === 'connection-actions' ? 'Choose an action...' : 'Search for resources, connections, runbooks...';

  const rightSection = badgeLabel
    ? <Badge
        variant="light"
        color="gray"
        rightSection={
          <ActionIcon size={12} variant="transparent" color="gray" onClick={back}>
            <X size={12} />
          </ActionIcon>
        }
      >
        {badgeLabel}
      </Badge>
    : null;

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
        placeholder={placeholder}
        onKeyDown={handleKeyDown}
        rightSection={rightSection}
        rightSectionWidth={150}
      />
      <SpotlightActionsList>
        {currentPage === 'main' && <MainPage query={debouncedQuery} />}
        {currentPage === 'resource-roles' && <ResourceRolesPage />}
        {currentPage === 'connection-actions' && <ConnectionActionsPage />}
      </SpotlightActionsList>
    </SpotlightRoot>
  );
}

export default CommandPalette;
