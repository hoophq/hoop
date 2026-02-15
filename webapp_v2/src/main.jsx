import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import App from './App'

import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <MantineProvider>
      <Notifications />
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </MantineProvider>
  </StrictMode>,
)
