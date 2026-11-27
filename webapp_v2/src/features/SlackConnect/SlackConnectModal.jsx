import { useCallback, useEffect, useRef, useState } from 'react'
import { Group, Skeleton, Stack, Text } from '@mantine/core'
import { useClipboard } from '@mantine/hooks'
import { AlertCircle, Check, Copy } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import CodeSnippet from '@/components/CodeSnippet'
import Modal from '@/components/Modal'
import useCountdown from '@/hooks/useCountdown'
import { useLiveEvent } from '@/hooks/useLiveEvent'
import { LIVE_EVENTS } from '@/services/liveEvents'
import { usersService } from '@/services/users'
import { useUserStore } from '@/stores/useUserStore'
import { LINK_POLL_MS, formatExpiry } from './helpers'
import { useSlackConnectStore } from './store'

// Rendered only while open, so each opening asks for a new code.
export default function SlackConnectModal() {
  const userId = useUserStore((s) => s.user?.id)
  const disableClipboard = useUserStore((s) => s.disableClipboard)
  const closeModal = useSlackConnectStore((s) => s.closeModal)
  const confirmLinked = useSlackConnectStore((s) => s.confirmLinked)
  const clipboard = useClipboard({ timeout: 2000 })

  // { loading, link: { code, command, expires_at }, error }
  const [request, setRequest] = useState({ loading: true, link: null, error: null })
  // One request at a time. The gateway keeps the code of the last request it
  // stored, which two requests sent together do not show to be the last one
  // answered: the screen would hold a code that does not work. StrictMode runs
  // the effect below twice.
  const inflight = useRef(null)

  // Answers the next state.
  const requestCode = useCallback(() => {
    if (inflight.current) return inflight.current
    const pending = usersService
      .createSlackLinkCode()
      .then(
        ({ data }) => ({ loading: false, link: data, error: null }),
        (err) => ({ loading: false, link: null, error: { message: err.response?.data?.message || err.message } }),
      )
      .finally(() => {
        if (inflight.current === pending) inflight.current = null
      })
    inflight.current = pending
    return pending
  }, [])

  const getCode = async () => {
    setRequest({ loading: true, link: null, error: null })
    setRequest(await requestCode())
  }

  useEffect(() => {
    let closed = false
    async function loadCode() {
      const next = await requestCode()
      if (!closed) setRequest(next)
    }
    loadCode()
    return () => {
      closed = true
    }
  }, [requestCode])

  const { loading, link, error } = request
  const { expired } = useCountdown(link?.expires_at ?? null)
  const waiting = Boolean(link)

  useLiveEvent(LIVE_EVENTS.USER_SLACK_LINKED, (event) => {
    if (event.id === userId) confirmLinked()
  })

  // Kept after the code expires: a link made in its last seconds still closes the modal.
  useEffect(() => {
    if (!waiting) return undefined
    const timer = setInterval(async () => {
      try {
        const { data } = await usersService.getSlackLink()
        if (data?.linked) confirmLinked()
      } catch {
        // The next poll asks again.
      }
    }, LINK_POLL_MS)
    return () => clearInterval(timer)
  }, [waiting, confirmLinked])

  const live = link && !expired

  return (
    <Modal opened onClose={closeModal} title="Connect Slack">
      <Stack gap="md">
        {loading && (
          <Stack gap="sm" aria-busy="true" aria-label="Getting a code">
            <Skeleton h={20} w="70%" />
            <Skeleton h={44} />
            <Skeleton h={20} w="50%" />
          </Stack>
        )}

        {error && (
          <Alert icon={<AlertCircle size={16} />} color="red" title="Failed to get a code.">
            <Stack gap="xs" align="flex-start">
              {error.message && <Text size="sm">{error.message}</Text>}
              <Button variant="default" size="sm" onClick={getCode}>
                Try again
              </Button>
            </Stack>
          </Alert>
        )}

        {live && (
          <>
            <Text size="sm">Send this command in Slack, in any channel or direct message:</Text>
            <CodeSnippet code={link.command} variant="gray" />
            <Text size="sm" c="dimmed">
              {`The code works once and expires at ${formatExpiry(link.expires_at)}.`}
            </Text>
          </>
        )}

        {link && expired && (
          <Stack gap="sm" align="flex-start">
            <Text size="sm" role="status">
              The code expired.
            </Text>
            <Button variant="default" onClick={getCode}>
              Get a new code
            </Button>
          </Stack>
        )}

        <Group justify="flex-end" gap="sm">
          <Button variant="subtle" color="gray" onClick={closeModal}>
            Close
          </Button>
          {live && !disableClipboard && (
            <Button
              leftSection={clipboard.copied ? <Check size={16} /> : <Copy size={16} />}
              onClick={() => clipboard.copy(link.command)}
            >
              {clipboard.copied ? 'Copied' : 'Copy command'}
            </Button>
          )}
        </Group>
      </Stack>
    </Modal>
  )
}
