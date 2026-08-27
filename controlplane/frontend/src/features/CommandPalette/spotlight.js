import { spotlight } from '@mantine/spotlight';

// Kept out of CommandPaletteRoot.jsx so that file exports only its component —
// mixing component and non-component exports breaks Fast Refresh.
export { spotlight };

export const openCommandPalette = () => spotlight.open();
