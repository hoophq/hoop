import { Info } from 'lucide-react'
import Alert from '@/components/Alert'

// RDP has no client-side credentials to hand out — the browser client posts the
// credential to /rdpproxy/client itself (see the "Open web client" footer
// action in SessionPanel).
export function RdpCredentials() {
  return (
    <Alert color="sky" icon={<Info size={16} />}>
      Works only with Web Client
    </Alert>
  )
}
