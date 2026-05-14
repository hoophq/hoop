import { SpotlightRoot, SpotlightSearch, SpotlightActionsList, spotlight } from '@mantine/spotlight';
import { Badge, ActionIcon } from '@mantine/core';
import { Search, X } from 'lucide-react';

export { spotlight };

export const openCommandPalette = () => spotlight.open();

function CommandPaletteRoot({ 
  query, 
  onQueryChange, 
  onClose, 
  onKeyDown,
  currentPage,
  context,
  children 
}) {
  const badgeLabel =
    currentPage === 'resource-roles' ? context.resource?.name :
    currentPage === 'connection-actions' ? context.connection?.name :
    null

  const placeholder =
    currentPage === 'resource-roles'
      ? 'Select a connection...'
      : currentPage === 'connection-actions' 
        ? 'Choose an action...' 
        : 'Search for resources, connections, runbooks...';

  const rightSection = badgeLabel
    ? <Badge
        variant="light"
        color="gray"
        rightSection={
          <ActionIcon size={12} variant="transparent" color="gray" onClick={onKeyDown}>
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
      onQueryChange={onQueryChange}
      onSpotlightClose={onClose}
      shortcut={['mod + K']}
      scrollable
      maxHeight={400}
      clearQueryOnClose={false}
    >
      <SpotlightSearch
        leftSection={<Search size={16} />}
        placeholder={placeholder}
        onKeyDown={onKeyDown}
        rightSection={rightSection}
        rightSectionWidth={150}
      />
      <SpotlightActionsList>
        {children}
      </SpotlightActionsList>
    </SpotlightRoot>
  );
}

export default CommandPaletteRoot;
