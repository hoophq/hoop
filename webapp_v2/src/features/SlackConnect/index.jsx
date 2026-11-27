import { useEffect } from 'react'
import { useSlackConnectStore } from './store'
import { useSlackConnect } from './useSlackConnect'
import SlackConnectModal from './SlackConnectModal'

export { default as SlackConnectPrompt } from './SlackConnectPrompt'

// Mounted in the gateway layout: asks once whether the user can link Slack and
// holds the one "Connect Slack" modal, opened by the prompt and the user menu.
export default function SlackConnect() {
  const { enabled, user } = useSlackConnect()
  const opened = useSlackConnectStore((s) => s.modalOpened)
  const userId = user?.id
  const linked = Boolean(user?.slack_id)

  useEffect(() => {
    if (enabled && userId && !linked) useSlackConnectStore.getState().loadStatus(userId)
  }, [enabled, userId, linked])

  if (!enabled || !opened) return null
  return <SlackConnectModal />
}
