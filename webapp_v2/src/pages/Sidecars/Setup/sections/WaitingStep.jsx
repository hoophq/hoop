import { useEffect, useState } from 'react'
import { Box, Group, Loader, Stack, Text, ThemeIcon } from '@mantine/core'
import { Check, X } from 'lucide-react'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import { sidecarsService } from '@/services/sidecars'
import classes from './WaitingStep.module.css'

// Foreground polls are cheap (one GET, in-memory answer); a hidden tab needs
// far fewer.
const POLL_MS = 3000
const POLL_HIDDEN_MS = 15000

const STATE = {
  waiting: {
    title: 'Waiting for the sidecar to connect',
    description:
      'This can take up to 5 minutes. The sidecar dials out and nothing dials in, so check outbound HTTPS from the host it runs on.',
  },
  connected: {
    title: 'Sidecar connected',
    description: 'It checked in and took its configuration.',
  },
  gone: {
    title: 'This sidecar was deleted',
    description: 'It was removed from the list while this page was open.',
  },
}

/**
 * Step 2 of the sidecar wizard. Polls the sidecar until the control plane
 * records its first check-in, then hands the fresh record back through
 * onConnected. Nothing here reaches the sidecar: it is the side that dials
 * out, so the page also offers to continue without waiting.
 *
 * A banner rather than the Figma's full-height box, because the blocks of step
 * one stay on screen behind it. The token is shown once and this page is the
 * only place it exists, so a waiting state that covered it would strand
 * anyone who had not copied it yet.
 *
 * Cancel asks before deleting: the token may already sit in a deployment that
 * is rolling out, and deleting revokes it for good.
 */
export default function WaitingStep({ sidecar, onConnected, onGone, onDelete, onKeep, deleting }) {
  const [connected, setConnected] = useState(!!sidecar?.last_seen_at)
  const [gone, setGone] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)

  useEffect(() => {
    if (connected || gone || !sidecar?.id) return
    let cancelled = false
    let timer
    const tick = async () => {
      try {
        const fresh = await sidecarsService.get(sidecar.id)
        if (cancelled) return
        if (fresh.last_seen_at) {
          setConnected(true)
          onConnected(fresh)
          return
        }
      } catch (err) {
        if (cancelled) return
        if (err.response?.status === 404) {
          setGone(true)
          onGone()
          return
        }
      }
      timer = setTimeout(tick, document.visibilityState === 'hidden' ? POLL_HIDDEN_MS : POLL_MS)
    }
    tick()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [sidecar?.id, connected, gone, onConnected, onGone])

  const copy = connected ? STATE.connected : gone ? STATE.gone : STATE.waiting

  return (
    <>
      <Modal opened={confirmOpen} onClose={() => setConfirmOpen(false)} title="Cancel the setup?" size="sm">
        <Stack>
          <Text size="sm">
            {`The sidecar "${sidecar?.name}" was created and its token shown. Deleting it revokes that token; keeping it leaves the sidecar in the list as Waiting.`}
          </Text>
          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={onKeep} disabled={deleting}>
              Keep and go to list
            </Button>
            <Button color="red" onClick={onDelete} loading={deleting}>
              Delete sidecar
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Box className={classes.box} data-connected={connected || undefined} p="md">
        <Group wrap="nowrap" gap="md" align="center">
          {connected ? (
            <ThemeIcon size={32} radius="xl" color="green" variant="light" flex="0 0 auto">
              <Check size={18} aria-hidden="true" />
            </ThemeIcon>
          ) : (
            <Loader size="sm" color="gray" flex="0 0 auto" />
          )}

          <Stack gap={2} flex={1} miw={0}>
            <Text fw={600} size="sm">
              {copy.title}
            </Text>
            <Text size="xs" c="dimmed">
              {copy.description}
            </Text>
          </Stack>

          {!connected && !gone && (
            <Button
              color="red"
              variant="light"
              leftSection={<X size={16} aria-hidden="true" />}
              onClick={() => setConfirmOpen(true)}
              flex="0 0 auto"
            >
              Cancel
            </Button>
          )}
        </Group>
      </Box>
    </>
  )
}
