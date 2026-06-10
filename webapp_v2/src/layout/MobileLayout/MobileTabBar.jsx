import { Group } from '@mantine/core'
import { useLocation } from 'react-router-dom'
import MobileTabLink from './MobileTabLink'
import { TABS, TAB_BAR_HEIGHT } from './constants'

function isActive(pathname, path) {
  if (path === '/m') return pathname === '/m' || pathname === '/m/'
  return pathname.startsWith(path)
}

function MobileTabBar() {
  const { pathname } = useLocation()

  return (
    <Group grow gap={0} h={TAB_BAR_HEIGHT} wrap="nowrap">
      {TABS.map((tab) => (
        <MobileTabLink key={tab.path} tab={tab} active={isActive(pathname, tab.path)} />
      ))}
    </Group>
  )
}

export default MobileTabBar
