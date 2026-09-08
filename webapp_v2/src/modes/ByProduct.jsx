import { useModeConfig } from '@/modes'

/**
 * The one place a page choice depends on the product. Used in Router.jsx only:
 *
 *   <ByProduct gateway={<GatewayUsers />} controlPlane={<ControlPlaneUsers />} />
 *
 * `grep ByProduct src/Router.jsx` lists every page that differs between the
 * two products. Pages themselves never read the mode.
 */
export default function ByProduct({ gateway, controlPlane }) {
  const { id } = useModeConfig()
  return id === 'control-plane' ? controlPlane : gateway
}
