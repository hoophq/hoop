import { useMediaQuery } from '@mantine/hooks'

// Complement of the desktop breakpoint used in layout/Layout.jsx (min-width: 769px).
const MOBILE_VIEWPORT_QUERY = '(max-width: 768px)'

// True on phone-sized viewports. Routing decisions depend on this value, so it
// reads matchMedia synchronously on the first render (never starts undefined)
// and stays reactive to resizes.
export function useIsMobileViewport() {
  return useMediaQuery(MOBILE_VIEWPORT_QUERY, false, { getInitialValueInEffect: false })
}
