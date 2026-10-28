import { NAV, QUICK_ACCESS_ITEMS, SUGGESTION_ITEMS } from '@/layout/Sidebar/gatewayNav'
import { AGENTS_FLAG } from './agents'

// A flagged group flags its children.
const flaggedPaths = (items, inherited = false) =>
  items.flatMap((item) => {
    const flagged = inherited || item.featureFlag === AGENTS_FLAG
    const own = flagged && item.path ? [item.path.split('?')[0]] : []
    return [...own, ...flaggedPaths(item.children ?? [], flagged)]
  })

export const AGENT_PATHS = [
  ...new Set(flaggedPaths([...NAV.flatMap((section) => section.items), ...SUGGESTION_ITEMS, ...QUICK_ACCESS_ITEMS])),
]

const under = (pathname, path) => pathname === path || pathname.startsWith(`${path}/`)

// The agent landings and every page the flag hides from the menu, with its sub-pages.
export const isAgentPath = (pathname) =>
  pathname === '/' || pathname.startsWith('/onboarding') || AGENT_PATHS.some((path) => under(pathname, path))
