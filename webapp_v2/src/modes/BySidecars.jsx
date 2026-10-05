import { useSidecarsEnabled } from './sidecars'

// Picks a route's variant by useSidecarsEnabled(), as ByProduct does by mode.
export default function BySidecars({ on, off }) {
  return useSidecarsEnabled() ? on : off
}
