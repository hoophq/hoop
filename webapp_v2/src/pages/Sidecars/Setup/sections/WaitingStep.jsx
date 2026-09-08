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

/**
 * Step 2 of the sidecar wizard (Figma: "Create and deploy a new Sidecar |
 * Configure"). Polls the sidecar until the gateway records a handshake, then
 * hands the fresh record back through onConnected. No sidecar calls the
 * handshake yet, so the page also offers to continue without waiting.
 *
 * Cancel asks before deleting: the token may already sit in a deployment that
 * is rolling out, and deleting revokes it for good.
 */
export default function WaitingStep({ sidecar, onConnected, onDelete, onKeep, deleting }) {
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
  }, [sidecar?.id, connected, gone, onConnected])

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

      <Box className={classes.box} data-connected={connected || undefined}>
        <Stack align="center" gap="lg" py="xl">
          {connected ? (
            <ThemeIcon size={48} radius="xl" color="green" variant="light">
              <Check size={24} aria-hidden="true" />
            </ThemeIcon>
          ) : (
            <Loader size="md" color="gray" />
          )}
          <Stack gap={4} align="center">
            <Text fw={700}>{connected ? 'Sidecar connected' : gone ? 'This sidecar was deleted' : 'Waiting for the Sidecar to connect'}</Text>
            <Text size="sm" c="dimmed" ta="center">
              {connected
                ? 'The control plane recorded its handshake.'
                : gone
                  ? 'It was removed from the list while this page was open.'
                  : 'This can take up to 5 minutes. Start the sidecar with the values from the previous step.'}
            </Text>
          </Stack>
          {!connected && !gone && (
            <Button color="red" variant="light" leftSection={<X size={18} aria-hidden="true" />} onClick={() => setConfirmOpen(true)}>
              Cancel
            </Button>
          )}
        </Stack>
      </Box>
    </>
  )
}
