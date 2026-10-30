import { useCallback, useMemo, useRef } from 'react'
import { Box, Center, Text } from '@mantine/core'
import { AgGridReact } from 'ag-grid-react'
import { AllCommunityModule, ModuleRegistry, iconOverrides, themeAlpine } from 'ag-grid-community'
import { useUserStore } from '@/stores/useUserStore'
import { columnDefs, rowData } from './output'

// ag-grid is the heaviest dependency of the app: ExecOutput loads this module
// lazily, and nothing else imports ag-grid.
ModuleRegistry.registerModules([AllCommunityModule])

// lucide `funnel`, as the previous page drew the filter icon.
const FILTER_ICON_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10 20a1 1 0 0 0 .553.895l2 1A1 1 0 0 0 14 21v-7a2 2 0 0 1 .517-1.341L21.74 4.67A1 1 0 0 0 21 3H3a1 1 0 0 0-.742 1.67l7.225 7.989A2 2 0 0 1 10 14z"/></svg>'

// Figma "Output | Table": 36px rows, a bold header on a faint fill, the theme's
// borders and radius.
const THEME = themeAlpine
  .withPart(iconOverrides({ type: 'image', mask: true, icons: { filter: { svg: FILTER_ICON_SVG } } }))
  .withParams({
    fontFamily: 'inherit',
    fontSize: 14,
    headerFontSize: 14,
    headerFontWeight: 700,
    rowHeight: 36,
    headerHeight: 36,
    foregroundColor: 'var(--mantine-color-text)',
    headerBackgroundColor: 'var(--mantine-color-gray-0)',
    backgroundColor: 'var(--mantine-color-body)',
    borderColor: 'var(--mantine-color-gray-3)',
    wrapperBorderRadius: 'var(--mantine-radius-md)',
    oddRowBackgroundColor: 'var(--mantine-color-body)',
    accentColor: 'var(--mantine-color-indigo-6)',
  })

const DEFAULT_COL_DEF = { resizable: true, sortable: true, filter: true }

// The previous page paged the grid above 100 rows, 20 per page.
const PAGINATE_ABOVE = 100
const PAGE_SIZE = 20
const ROW_HEIGHT = 36
const PAGINATION_HEIGHT = 48
const MAX_HEIGHT = 432

export default function ResultsTable({ heads, body }) {
  const disableClipboard = useUserStore((s) => s.disableClipboard)
  const columns = useMemo(() => columnDefs(heads), [heads])
  const rows = useMemo(() => rowData(heads, body), [heads, body])
  const pagination = rows.length > PAGINATE_ABOVE
  const visibleRows = pagination ? PAGE_SIZE : rows.length
  const height = Math.min(
    ROW_HEIGHT * (visibleRows + 1) + 2 + (pagination ? PAGINATION_HEIGHT : 0),
    MAX_HEIGHT,
  )

  // Columns fit their content; when that leaves room, they stretch to the width
  // (Figma "Output | Table").
  const boxRef = useRef(null)
  const onFirstDataRendered = useCallback(({ api }) => {
    api.autoSizeAllColumns()
    const used = api.getAllDisplayedColumns().reduce((sum, column) => sum + column.getActualWidth(), 0)
    if (used < (boxRef.current?.clientWidth ?? 0)) api.sizeColumnsToFit()
  }, [])

  if (!heads.length || !body.length) {
    return (
      <Center py="xl">
        <Text c="dimmed">No results available</Text>
      </Center>
    )
  }

  return (
    <Box ref={boxRef} component="section" h={height} w="100%" aria-label="Query results table">
      <AgGridReact
        theme={THEME}
        columnDefs={columns}
        rowData={rows}
        defaultColDef={DEFAULT_COL_DEF}
        enableCellTextSelection={!disableClipboard}
        ensureDomOrder={!disableClipboard}
        pagination={pagination}
        paginationPageSize={PAGE_SIZE}
        onFirstDataRendered={onFirstDataRendered}
      />
    </Box>
  )
}
