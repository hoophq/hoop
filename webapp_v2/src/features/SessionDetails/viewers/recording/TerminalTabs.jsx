import { lazy, Suspense, useMemo, useState } from 'react'
import { Center, Loader, Stack } from '@mantine/core'
import ViewOptions from '../../components/ViewOptions'

const AsciinemaPlayer = lazy(() => import('./AsciinemaPlayer'))

const VIEWS = [
  { value: 'logs', label: 'Logs' },
  { value: 'video', label: 'Video' },
]

// Figma "Output | Logs" and "Output | Video": the terminal text, or a replay.
// `logs` is the Logs view. `getVideo` returns the replay ({ width, height,
// events }); it runs when the Video view opens, and must keep its identity
// while the recording does.
export default function TerminalTabs({ logs, getVideo }) {
  const [view, setView] = useState('logs')
  const video = useMemo(() => (view === 'video' ? getVideo() : null), [view, getVideo])
  return (
    <Stack gap="md">
      <ViewOptions views={VIEWS} value={view} onChange={setView} />
      {view === 'logs' ? (
        logs
      ) : (
        <Suspense
          fallback={
            <Center h={600}>
              <Loader size="sm" />
            </Center>
          }
        >
          <AsciinemaPlayer {...video} />
        </Suspense>
      )}
    </Stack>
  )
}
