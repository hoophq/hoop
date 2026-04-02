import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { MantineProvider, createTheme } from '@mantine/core';
import { Spotlight, SpotlightActionsList } from '@mantine/spotlight';
import { Notifications } from '@mantine/notifications';
import spotlightStyles from '@/features/spotlight.module.css';
import App from './App';

import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import '@mantine/spotlight/styles.css';

// Radix UI Indigo scale mapped to Mantine's 10-step array (steps 1–10)
// primaryShade: 8 → indigo-9 (#3e63dd) as the primary action color
const indigoScale = [
  '#fdfdfe', // indigo-1
  '#f7f9ff', // indigo-2
  '#edf2fe', // indigo-3
  '#e1e9ff', // indigo-4
  '#d2deff', // indigo-5
  '#c1d0ff', // indigo-6
  '#abbdf9', // indigo-7
  '#8da4ef', // indigo-8
  '#3e63dd', // indigo-9  ← primary
  '#3358d4' // indigo-10
];

const theme = createTheme({
  primaryColor: 'indigo',
  primaryShade: 8,
  defaultRadius: 'md',
  fontFamily: 'Inter, system-ui, -apple-system, sans-serif',
  colors: {
    indigo: indigoScale
  },
  fontSizes: {
    xs: '12px',
    sm: '14px',
    md: '16px',
    lg: '18px',
    xl: '20px'
  },
  lineHeights: {
    xs: 1.333,
    sm: 1.429,
    md: 1.5,
    lg: 1.444,
    xl: 1.4
  },
  components: {
    Spotlight: Spotlight.extend({
      classNames: {
        actionsGroup: spotlightStyles.actionsGroup,
        actionLabel: spotlightStyles.actionLabel,
        actionsList: spotlightStyles.actionsList
      }
    })
  }
});

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <MantineProvider theme={theme}>
      <Notifications />
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </MantineProvider>
  </StrictMode>
);
