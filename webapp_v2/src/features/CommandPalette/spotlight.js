import { spotlight } from '@mantine/spotlight';
import { clojureDispatch } from '@/utils/clojureDispatch';

// The Mantine Spotlight is only mounted on React-owned routes. On CLJS routes
// (where `react-shell` is active) the CLJS app renders its own command palette,
// so delegate there via re-frame dispatch.
export const openCommandPalette = () => {
  if (localStorage.getItem('react-shell') === 'true') {
    clojureDispatch('command-palette->toggle');
    return;
  }
  spotlight.open();
};
