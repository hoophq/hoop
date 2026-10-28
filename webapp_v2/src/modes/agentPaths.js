import { matchPath } from 'react-router-dom'
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

// The pathname as React Router reads it before it matches a route (decodePath).
const decode = (pathname) => {
  try {
    return pathname
      .split('/')
      .map((segment) => decodeURIComponent(segment).replace(/\//g, '%2F'))
      .join('/')
  } catch {
    return pathname
  }
}

// matchPath ignores case, like the route table.
const under = (pathname, path) => matchPath({ path, end: false }, pathname) !== null

// The agent landings and every page the flag hides from the menu, with its sub-pages.
export const isAgentPath = (rawPathname) => {
  const pathname = decode(rawPathname)
  return (
    matchPath('/', pathname) !== null ||
    under(pathname, '/onboarding') ||
    AGENT_PATHS.some((path) => under(pathname, path))
  )
}
