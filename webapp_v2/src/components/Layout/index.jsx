import { AppShell } from '@mantine/core'
import { useUIStore } from '@/stores/useUIStore'
import Sidebar from './Sidebar'
import Header from './Header'

function Layout({ children }) {
  const { sidebarOpen } = useUIStore()

  return (
    <AppShell
      header={{ height: 60 }}
      navbar={{
        width: 250,
        breakpoint: 'sm',
        collapsed: { mobile: !sidebarOpen, desktop: !sidebarOpen },
      }}
      padding="md"
    >
      <AppShell.Header>
        <Header />
      </AppShell.Header>

      <AppShell.Navbar>
        <Sidebar />
      </AppShell.Navbar>

      <AppShell.Main>{children}</AppShell.Main>
    </AppShell>
  )
}

export default Layout
