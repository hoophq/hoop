import { Suspense, lazy, useMemo, useState } from 'react'
import { Box, Group, Loader, Stack } from '@mantine/core'
import Tabs from '@/components/Tabs'
import LogsContainer from './LogsContainer'
import ResultsDownloadMenu from './ResultsDownloadMenu'
import { SQL_SUBTYPES, resultsToMatrix } from './resultsMatrix'

/**
 * ag-grid is the single heaviest dependency in the app and only the SQL "Table"
 * tab reaches it, so it is deferred exactly like the Dashboard defers recharts
 * (see the note in Router.jsx). Nobody who does not open that tab downloads it.
 */
const AgGridTable = lazy(() => import('./AgGridTable'))

const PLAIN_TEXT = 'Plain text'
const TABLE = 'Table'

/**
 * Port of `audit/views/results_container.cljs` (94 LOC).
 *
 * SQL output gets two tabs with a download menu beside them; everything else
 * gets the plain log view with the menu above it. A non-success status always
 * falls to the plain view, whatever the subtype.
 */
export default function ResultsContainer({
  connectionSubtype,
  results,
  resultsStatus,
  fixedHeight = false,
  sessionId,
  connectionName,
  hasLargePayload = false,
}) {
  const [tab, setTab] = useState(PLAIN_TEXT)

  const matrix = useMemo(
    () => resultsToMatrix(results, connectionSubtype),
    [results, connectionSubtype]
  )
  const heads = matrix?.[0]
  const body = useMemo(() => matrix?.slice(1), [matrix])

  const isSql = SQL_SUBTYPES.has(connectionSubtype)
  const tabular = Boolean(isSql && heads?.length && body?.length)

  // v1 only builds the download props on success, so the menu is absent while
  // the payload is still loading or errored.
  const downloadMenu =
    resultsStatus === 'success' ? (
      <ResultsDownloadMenu
        results={results}
        tabular={tabular}
        heads={heads}
        body={body}
        sessionId={sessionId}
        connectionName={connectionName}
        hasLargePayload={hasLargePayload}
      />
    ) : null

  const logs = <LogsContainer status={resultsStatus} logs={results} />

  if (resultsStatus !== 'success' || !isSql) {
    return (
      <Stack gap="xs" h={fixedHeight ? 384 : '100%'} mih={fixedHeight ? 384 : undefined}>
        {downloadMenu && <Group justify="flex-end">{downloadMenu}</Group>}
        <Box flex={1} mih={0}>
          {logs}
        </Box>
      </Stack>
    )
  }

  return (
    <Stack gap="xs" h={384} mih={384}>
      <Group justify="space-between" align="center" gap="md" wrap="nowrap">
        <Box flex={1} miw={0}>
          <Tabs value={tab} onChange={setTab}>
            <Tabs.List>
              <Tabs.Tab value={PLAIN_TEXT}>{PLAIN_TEXT}</Tabs.Tab>
              <Tabs.Tab value={TABLE}>{TABLE}</Tabs.Tab>
            </Tabs.List>
          </Tabs>
        </Box>
        {downloadMenu}
      </Group>

      <Box flex={1} mih={0}>
        {tab === PLAIN_TEXT ? (
          logs
        ) : (
          <Suspense fallback={<Loader size="sm" />}>
            <AgGridTable
              heads={heads}
              body={body}
              height="100%"
              theme="alpine"
              // v1 only paginates past 100 rows.
              pagination={(body?.length ?? 0) > 100}
              autoSizeColumns
            />
          </Suspense>
        )}
      </Box>
    </Stack>
  )
}
