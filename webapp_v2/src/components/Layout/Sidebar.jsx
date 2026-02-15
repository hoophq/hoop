import { NavLink, Stack, Text } from '@mantine/core'
import { useLocation, useNavigate } from 'react-router-dom'

const navItems = [
  { label: 'Dashboard', path: '/' },
  { label: 'Resources', path: '/resources' },
  { label: 'Connections', path: '/connections' },
  { label: 'Agents', path: '/agents' },
  { label: 'Guardrails', path: '/guardrails' },
  {
    label: 'Features',
    children: [
      { label: 'Access Control', path: '/features/access-control' },
      { label: 'Runbooks', path: '/features/runbooks' },
      { label: 'Data Masking', path: '/features/data-masking' },
    ],
  },
  { label: 'Integrations', path: '/integrations/authentication' },
  { label: 'Plugins', path: '/plugins' },
  {
    label: 'Settings',
    children: [
      { label: 'License', path: '/settings/license' },
      { label: 'Infrastructure', path: '/settings/infrastructure' },
    ],
  },
  { label: 'Users', path: '/organization/users' },
  { label: 'Sessions', path: '/sessions' },
  { label: 'Reviews', path: '/reviews' },
]

function Sidebar() {
  const location = useLocation()
  const navigate = useNavigate()

  const renderNavItem = (item) => {
    if (item.children) {
      return (
        <NavLink key={item.label} label={item.label}>
          {item.children.map((child) => (
            <NavLink
              key={child.path}
              label={child.label}
              active={location.pathname === child.path}
              onClick={() => navigate(child.path)}
            />
          ))}
        </NavLink>
      )
    }

    return (
      <NavLink
        key={item.path}
        label={item.label}
        active={location.pathname === item.path}
        onClick={() => navigate(item.path)}
      />
    )
  }

  return (
    <Stack gap={0} p="md">
      <Text size="lg" fw={700} mb="md">
        Hoop
      </Text>
      {navItems.map(renderNavItem)}
    </Stack>
  )
}

export default Sidebar
