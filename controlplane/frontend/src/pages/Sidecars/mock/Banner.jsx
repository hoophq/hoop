import { Info } from 'lucide-react'
import Alert from '@/components/Alert'

/**
 * Part of the mock. Goes when `mock/` goes — see mock/index.js.
 *
 * It exists because the rest of this repo refuses to ship a page that looks
 * loaded with nothing behind it (see components/NotImplemented). A mock is the
 * same lie unless it says so on the screen.
 */
export default function MockBanner() {
  return (
    <Alert color="yellow" icon={<Info size={18} />} title="This page is a mock">
      {'Every row below is hard-coded in pages/Sidecars/mock/. There is no fleet API yet — GET /api/fleet is EVL-232 and answers 501 today. Nothing here reflects a real sidecar.'}
    </Alert>
  )
}
