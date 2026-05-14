import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { theme, cssVariablesResolver } from '@/theme';
import App from './App';

import '@mantine/core/styles.layer.css';
import '@mantine/notifications/styles.layer.css';
import '@mantine/spotlight/styles.layer.css';
import '@mantine/dates/styles.layer.css';
import './layers.css';

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="light" cssVariablesResolver={cssVariablesResolver}>
      <Notifications />
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </MantineProvider>
  </StrictMode>
);
