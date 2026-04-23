import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { theme } from '@/theme';
import App from './App';

import '@mantine/core/styles.layer.css';
import '@mantine/notifications/styles.layer.css';
import '@mantine/spotlight/styles.layer.css';
import './layers.css';

function cssVariables(theme) {
  return {
    variables: {
      '--sidebar-bg': '#182449',
      '--sidebar-border': 'rgba(255, 255, 255, 0.1)',
      // Mantine default dimmed = gray.6 (#d9d9e0) — fails WCAG AA on white.
      // Override to slate11 (gray[8] = #60646c, ~7:1 contrast) to match Radix --gray-11.
      '--mantine-color-dimmed': theme.colors.gray[8],
    },
    light: {},
    dark: {}
  };
}

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="light" cssVariablesResolver={cssVariables}>
      <Notifications />
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </MantineProvider>
  </StrictMode>
);
