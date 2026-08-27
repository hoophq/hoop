import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { MantineProvider } from '@mantine/core';
import { theme, cssVariablesResolver } from '@/theme';
import App from './App';

// No layers.css here. webapp_v2 declares `@layer legacy-reset, mantine, app` to push
// the ClojureScript Tailwind preflight below Mantine's own component CSS. There is no
// ClojureScript stylesheet in this app, so the layer has nothing to hold and Mantine's
// styles.layer.css orders itself.
import '@mantine/core/styles.layer.css';
import '@mantine/spotlight/styles.layer.css';

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <MantineProvider
      theme={theme}
      defaultColorScheme="light"
      cssVariablesResolver={cssVariablesResolver}
    >
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </MantineProvider>
  </StrictMode>
);
