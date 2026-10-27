import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Anchor, Divider, Group, Loader, Paper, Stack, Text, ThemeIcon } from '@mantine/core'
import { Check } from 'lucide-react'
import Button from '@/components/Button'
import { useLiveEvent } from '@/hooks/useLiveEvent'
import { LIVE_EVENTS } from '@/services/liveEvents'
import { sidecarsService } from '@/services/sidecars'

// The live stream is best effort; a slow poll makes sure the page catches up.
const POLL_MS = 10000

/**
 * What an approved sidecar does next. The setup happens in the terminal that
 * printed the code: the config stays in a file on that machine, and Hoop
 * shows it once the sidecar runs it. Configuring it here instead is offered,
 * as the second way.
 *
 * The plane records a check-in only from a sidecar that runs a config, so
 * "checked in" here means the terminal setup is done and the sidecar is up.
 */
export default function FinishInTerminal({ sidecar }) {
  const navigate = useNavigate()
  const page = `/sidecars/${sidecar.id}`
  const [running, setRunning] = useState(Boolean(sidecar.last_seen_at))

  const refresh = useCallback(async () => {
    try {
      const { data } = await sidecarsService.get(sidecar.id)
      if (data.last_seen_at) setRunning(true)
    } catch {
      // The next check-in or poll tries again.
    }
  }, [sidecar.id])

  useLiveEvent(
    LIVE_EVENTS.SIDECAR_CHECKED_IN,
    (event) => {
      if (event.id === sidecar.id) refresh()
    },
    !running,
  )

  useEffect(() => {
    if (running) return undefined
    const timer = setInterval(refresh, POLL_MS)
    return () => clearInterval(timer)
  }, [running, refresh])

  return (
    <Paper bg="gray.0" radius="xl" p="lg">
      <Stack gap="md">
        <Group gap="sm" wrap="nowrap">
          <ThemeIcon size={32} radius="xl" color="green" variant="light">
            <Check size={18} aria-hidden="true" />
          </ThemeIcon>
          <Text fw={600}>{`"${sidecar.name}" is approved`}</Text>
        </Group>

        {running ? (
          <Stack gap={4}>
            <Text fw={600}>Setup finished in your terminal</Text>
            <Text size="sm" c="dimmed">
              The sidecar runs the config file on that machine. Hoop shows it on the sidecar page.
            </Text>
          </Stack>
        ) : (
          <Stack gap={4}>
            <Text fw={600}>Finish the setup in your terminal</Text>
            <Text size="sm" c="dimmed">
              Your terminal now shows the setup: pick what this sidecar protects and its rules. The config stays in a
              file on that machine, and Hoop shows it here once the sidecar runs it.
            </Text>
          </Stack>
        )}

        {running ? (
          <Group justify="space-between" wrap="wrap">
            <Group gap="xs">
              <ThemeIcon size={20} radius="xl" color="green" variant="light">
                <Check size={12} aria-hidden="true" />
              </ThemeIcon>
              <Text size="sm">Running. It checked in a moment ago.</Text>
            </Group>
            <Button onClick={() => navigate(page)}>Open the sidecar</Button>
          </Group>
        ) : (
          <Group gap="xs">
            <Loader size="sm" color="gray" />
            <Text size="sm" c="dimmed">
              Waiting for the setup to finish in your terminal…
            </Text>
          </Group>
        )}

        {!running && (
          <>
            <Divider />
            <Text size="sm" c="dimmed">
              {'Rather configure it here? As soon as it has a listener in Hoop, the terminal starts it.'}
            </Text>
            <Anchor component="button" type="button" size="sm" onClick={() => navigate(page)}>
              Configure it in Hoop
            </Anchor>
          </>
        )}
      </Stack>
    </Paper>
  )
}
