import { House, Inbox, GalleryVerticalEnd, BrainCog, Users } from 'lucide-react'

export const MOBILE_ADMIN_FLAG = 'experimental.mobile_admin'

export const TAB_BAR_HEIGHT = 56

export const TABS = [
  { label: 'Home', path: '/m', icon: House },
  { label: 'Reviews', path: '/m/reviews', icon: Inbox },
  { label: 'Sessions', path: '/m/sessions', icon: GalleryVerticalEnd },
  { label: 'Agents', path: '/m/agents', icon: BrainCog },
  { label: 'Users', path: '/m/users', icon: Users },
]
