import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import ModeThemeProvider from '@/modes/ModeThemeProvider';
import { useUserStore } from '@/stores/useUserStore';
import App from './App';

// layers.css MUST come first. A cascade layer takes its position from where the
// name is FIRST seen, and `@mantine/core/styles.layer.css` opens with
// `@layer mantine {`. Imported after it, the `@layer legacy-reset, mantine, app;`
// statement cannot move `mantine` — it only appends the unseen names after it,
// which puts legacy-reset ABOVE mantine and silently inverts the whole point of
// the ordering (a filled Button renders transparent on CLJS routes).
import './layers.css';
import '@mantine/core/styles.layer.css';
import '@mantine/spotlight/styles.layer.css';
import '@mantine/dates/styles.layer.css';
import '@mantine/charts/styles.layer.css';
import '@mantine/carousel/styles.layer.css';

// Signal to the parked ClojureScript bundle (which keeps a document-level
// keydown listener alive for its own command palette) that the React shell
// is in charge. Combined with __hoopReactShellCljsVisible toggled by
// ClojureApp.jsx, this lets the CLJS handler bail out on React-only routes
// instead of opening a second Radix dialog underneath the Mantine Spotlight.
window.__hoopReactShellPresent = true;

// Which product this bundle renders as (gateway or control plane) comes from the
// backend. Kicked off before the first render; the shell re-renders when it lands.
useUserStore.getState().loadAppMode();

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <ModeThemeProvider>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </ModeThemeProvider>
  </StrictMode>
);
