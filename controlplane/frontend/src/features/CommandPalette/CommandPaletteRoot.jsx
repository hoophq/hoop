import { SpotlightRoot, SpotlightSearch, SpotlightActionsList } from '@mantine/spotlight';
import { Search } from 'lucide-react';

// Flat. webapp_v2 pushed sub-pages here (resource → roles → actions) and showed the
// current one as a badge in the search field. Those pages existed to reach a
// connection, which is not something this app does.
function CommandPaletteRoot({ query, onQueryChange, onClose, children }) {
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
        placeholder="Jump to..."
      />
      <SpotlightActionsList>
        {children}
      </SpotlightActionsList>
    </SpotlightRoot>
  );
}

export default CommandPaletteRoot;
