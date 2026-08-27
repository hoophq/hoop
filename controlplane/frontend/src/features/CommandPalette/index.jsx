import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { spotlight } from '@mantine/spotlight';
import CommandPaletteRoot from './CommandPaletteRoot';
import MainPage from './MainPage';

function ConnectedCommandPalette() {
  const navigate = useNavigate();
  const [query, setQuery] = useState('');

  const handleNavigate = (path) => {
    spotlight.close();
    navigate(path);
  };

  return (
    <CommandPaletteRoot query={query} onQueryChange={setQuery} onClose={() => setQuery('')}>
      <MainPage query={query} onNavigate={handleNavigate} />
    </CommandPaletteRoot>
  );
}

export default ConnectedCommandPalette;
