import { useState } from 'react'
import { useUserStore } from '@/stores/useUserStore'
import { licenseService } from '@/services/license'
import { isHostNotAllowedError } from '@/utils/license'
import { showSnackbar } from '@/utils/snackbar'
import { HOST_HINT } from './constants'

// The one save path for a license, shared by the modal and anything else that
// takes a pasted document. Order matters: parse, PUT, refresh, then report.
// The success snackbar waits for the refresh so the page never says "installed"
// while still rendering the old state.
export function useLicenseUpdate() {
  const refreshServerInfo = useUserStore((state) => state.refreshServerInfo)
  const [saving, setSaving] = useState(false)

  async function save(text) {
    let parsed
    try {
      parsed = JSON.parse(text)
    } catch {
      showSnackbar({
        level: 'error',
        text: 'The license is not valid JSON.',
        description: 'Copy the whole document Hoop issued, braces included.',
      })
      return { ok: false }
    }

    setSaving(true)
    try {
      await licenseService.update(parsed)
    } catch (err) {
      // On 401 the api interceptor is already redirecting to /login.
      if (err.response?.status !== 401) {
        const message = err.response?.data?.message
        showSnackbar({
          level: 'error',
          text: 'Failed to install license',
          description: isHostNotAllowedError(message) ? `${message}. ${HOST_HINT}` : message,
        })
      }
      setSaving(false)
      return { ok: false }
    }

    // The license is active server-side from here on. A refresh failure only
    // means the in-memory state is stale.
    const refreshed = await refreshServerInfo()
    setSaving(false)
    if (refreshed) {
      showSnackbar({ level: 'success', text: 'License installed' })
    } else {
      showSnackbar({
        level: 'info',
        text: 'License installed',
        description: 'Reload the page to see the new state.',
      })
    }
    return { ok: true }
  }

  return { save, saving }
}
