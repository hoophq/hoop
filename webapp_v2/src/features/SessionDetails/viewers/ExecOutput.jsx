import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { Center, Group, Loader, Stack, Text } from '@mantine/core'
import EmptyState from '@/layout/EmptyState'
import { sessionsService } from '@/services/sessions'
import { useUserStore } from '@/stores/useUserStore'
import ViewOptions from '../components/ViewOptions'
import DownloadMenu from './exec/DownloadMenu'
import PlainText from './exec/PlainText'
import PreviewUnavailable from '../components/PreviewUnavailable'
import { downloadOutput } from './exec/download'
import { SQL_SUBTYPES, decodeOutput, isTabular, parseMatrix, sanitizeOutput } from './exec/output'

const ResultsTable = lazy(() => import('./exec/ResultsTable'))

const VIEWS = [
  { value: 'table', label: 'Table' },
  { value: 'plain', label: 'Plain Text' },
]

function Loading({ text }) {
  return (
    <Center py="xl">
      <Group gap="xs">
        <Loader size="xs" />
        <Text size="sm" c="dimmed" fs="italic">
          {text}
        </Text>
      </Group>
    </Center>
  )
}

// Above 4 MB the session read does not expand the output: the gateway streams it
// from its own route.
function useLargeOutput(sessionId, largeOutput) {
  const [state, setState] = useState({ key: null, status: 'idle', data: null })
  useEffect(() => {
    if (!largeOutput) return undefined
    let cancelled = false
    sessionsService
      .resultStream(sessionId)
      .then(({ data }) => {
        if (!cancelled) setState({ key: sessionId, status: 'ready', data: typeof data === 'string' ? data : '' })
      })
      .catch(() => {
        if (!cancelled) setState({ key: sessionId, status: 'error', data: null })
      })
    return () => {
      cancelled = true
    }
  }, [sessionId, largeOutput])
  if (!largeOutput) return { status: 'idle', data: null }
  return state.key === sessionId ? state : { status: 'loading', data: null }
}

// The output of an exec (matrix §9 O02–O08, O18; §12 downloads): a table and
// plain text for SQL resources, plain text for the rest.
export default function ExecOutput({ session, largeOutput }) {
  const disableDownload = useUserStore((s) => s.disableSessionsDownload)
  const large = useLargeOutput(session.id, largeOutput)
  const subtype = session.connection_subtype
  const sql = SQL_SUBTYPES.has(subtype)
  // The gateway leaves the stream out for a user who may not read the output
  // (an approver who is not the owner): the session has bytes, the reply none.
  const withheld = !largeOutput && session.event_stream == null && (session.event_size ?? 0) > 0

  const output = useMemo(() => {
    const raw = largeOutput ? large.data : decodeOutput(session.event_stream?.[0])
    return raw == null ? null : sanitizeOutput(raw, subtype)
  }, [largeOutput, large.data, session.event_stream, subtype])

  const parsed = useMemo(() => (sql && output ? parseMatrix(output, subtype) : null), [sql, output, subtype])
  const tabular = isTabular(parsed)
  const [view, setView] = useState(null)
  const current = view || (tabular ? 'table' : 'plain')

  const downloadProps = {
    output: output || '',
    matrix: parsed?.matrix,
    sessionId: session.id,
    connectionName: session.connection,
    largeOutput,
  }

  if (largeOutput && large.status === 'loading') return <Loading text="Loading data for this session" />
  if (largeOutput && large.status === 'error') {
    return (
      <PreviewUnavailable
        size={session.event_size}
        message="This output could not be loaded here. Download it to view the contents."
        onDownload={disableDownload ? undefined : () => downloadOutput(downloadProps, 'txt')}
      />
    )
  }
  if (withheld) {
    return (
      <EmptyState
        compact
        title="The output is not visible to you"
        description="The owner of the session, admins and auditors can read it."
      />
    )
  }
  if (!output) return <EmptyState compact title="This session returned no output" />

  return (
    <Stack gap="md">
      <ViewOptions views={sql ? VIEWS : []} value={current} onChange={setView}>
        {!disableDownload && <DownloadMenu tabular={tabular} {...downloadProps} />}
      </ViewOptions>
      {current === 'table' ? (
        <Suspense fallback={<Loading text="Loading table" />}>
          <ResultsTable heads={parsed?.heads || []} body={parsed?.body || []} />
        </Suspense>
      ) : (
        <PlainText output={output} />
      )}
    </Stack>
  )
}
