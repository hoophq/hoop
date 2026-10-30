import { Suspense } from 'react'
import { Center, Loader, Stack } from '@mantine/core'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { outputViewer } from '../decoders/sessionFormat'
import { VIEWERS } from '../viewers'
import DataMasking from '../insights/DataMasking'

export default function OutputTab({ session }) {
  const largeOutput = useSessionDetailsStore((s) => s.largeOutput)
  const Viewer = VIEWERS[outputViewer(session)] || VIEWERS.raw
  return (
    <Stack gap="lgAlt">
      <DataMasking session={session} />
      <Suspense
        fallback={
          <Center py="xl">
            <Loader size="sm" />
          </Center>
        }
      >
        <Viewer session={session} largeOutput={largeOutput} />
      </Suspense>
    </Stack>
  )
}
