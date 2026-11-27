import { useUserStore } from '@/stores/useUserStore'
import { SLACK_CONNECT_FLAG, canConnectSlack, shouldShowPrompt } from './helpers'
import { useSlackConnectStore } from './store'

// What the prompt and the user-menu item read.
export function useSlackConnect() {
  const enabled = useUserStore((s) => Boolean(s.featureFlags?.[SLACK_CONNECT_FLAG]))
  const user = useUserStore((s) => s.user)
  const loadedStatus = useSlackConnectStore((s) => s.status)
  const statusUserId = useSlackConnectStore((s) => s.statusUserId)
  const dismissedKey = useSlackConnectStore((s) => s.dismissedKey)
  const openModal = useSlackConnectStore((s) => s.openModal)
  const dismissPrompt = useSlackConnectStore((s) => s.dismissPrompt)

  const status = statusUserId === user?.id ? loadedStatus : null
  return {
    enabled,
    user,
    canConnect: canConnectSlack({ enabled, user, status }),
    showPrompt: shouldShowPrompt({ enabled, user, status, dismissedKey }),
    openModal,
    dismissPrompt,
  }
}
