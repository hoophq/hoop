import { useCallback, useMemo } from 'react'
import { Box, Center, Group, Loader, Stack, Text } from '@mantine/core'
import { AgGridReact } from 'ag-grid-react'
import {
  AllCommunityModule,
  ModuleRegistry,
  colorSchemeDarkBlue,
  colorSchemeLightWarm,
  iconOverrides,
  themeAlpine,
  themeBalham,
  themeMaterial,
  themeQuartz,
} from 'ag-grid-community'
import { TriangleAlert } from 'lucide-react'

/**
 * Port of `webapp.components.ag-grid-table` (webapp/src/webapp/components/ag_grid_table.cljs).
 *
 * ag-grid is the single heaviest dependency in the bundle and only the SQL
 * "Table" tab of a session needs it, so this module must always be reached
 * through `React.lazy(() => import('@/pages/Sessions/components/AgGridTable'))`.
 * Nothing outside this file may import `ag-grid-community` / `ag-grid-react`.
 *
 * Everything below module scope runs once, on first lazy load — mirroring the
 * `defonce` theme constants in the CLJS plus the `ModuleRegistry` registration
 * that the CLJS does at app boot (webapp/src/webapp/app.cljs:817).
 */
ModuleRegistry.registerModules([AllCommunityModule])

/** lucide `funnel` icon, verbatim from the CLJS `icon-overrides`. */
const FILTER_ICON_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-funnel-icon lucide-funnel"><path d="M10 20a1 1 0 0 0 .553.895l2 1A1 1 0 0 0 14 21v-7a2 2 0 0 1 .517-1.341L21.74 4.67A1 1 0 0 0 21 3H3a1 1 0 0 0-.742 1.67l7.225 7.989A2 2 0 0 1 10 14z"/></svg>'

const ICON_OVERRIDES = iconOverrides({
  type: 'image',
  mask: true,
  icons: { filter: { svg: FILTER_ICON_SVG } },
})

const BASE_THEMES = {
  alpine: themeAlpine,
  balham: themeBalham,
  material: themeMaterial,
  quartz: themeQuartz,
}

/**
 * ag-grid 33 uses the Theming API (no theme stylesheet import — the CLJS build
 * imports none either). Themes are cached so the object identity stays stable
 * across mounts, otherwise ag-grid re-generates its stylesheet on every render.
 */
const themeCache = new Map()

function resolveTheme(name, darkMode) {
  const key = `${name}:${darkMode}`
  const cached = themeCache.get(key)
  if (cached) return cached

  const resolved = (BASE_THEMES[name] || themeAlpine)
    .withPart(darkMode ? colorSchemeDarkBlue : colorSchemeLightWarm)
    .withPart(ICON_OVERRIDES)

  themeCache.set(key, resolved)
  return resolved
}

const DEFAULT_COL_DEF = {
  resizable: true,
  sortable: true,
  filter: true,
  editable: true,
}

/**
 * Port of `normalize-row-data`: makes every row match the header count,
 * dropping empty cells first when the row is too wide and padding with empty
 * strings when it is too narrow.
 */
function normalizeRows(headers, rows) {
  const headerCount = headers.length

  return rows.map((row) => {
    if (row.length > headerCount) {
      const nonEmpty = row.filter(
        (cell) => cell != null && (typeof cell !== 'string' || cell !== '')
      )
      if (nonEmpty.length >= headerCount) return nonEmpty.slice(0, headerCount)
      return nonEmpty.concat(Array(headerCount - nonEmpty.length).fill(''))
    }

    if (row.length < headerCount) {
      return row.concat(Array(headerCount - row.length).fill(''))
    }

    return row
  })
}

/**
 * Port of the body of `main`: builds ag-grid column defs + row objects, or
 * reports the empty/error states the CLJS renders instead of the grid.
 */
function buildGrid(headers, rows) {
  const emptyData =
    !Array.isArray(headers) || headers.length === 0 || !Array.isArray(rows) || rows.length === 0

  if (emptyData) return { empty: true }

  try {
    const fields = headers.map((header) => (typeof header === 'string' ? header : String(header)))

    const columns = fields.map((field) => ({
      field,
      cellEditor: 'agTextCellEditor',
      headerName: field,
    }))

    const rowData = normalizeRows(headers, rows).map((row) =>
      row.reduce((acc, value, index) => {
        acc[fields[index]] = value
        return acc
      }, {})
    )

    return { columns, rowData }
  } catch (e) {
    const message = `Error processing data: ${e.message}`
    console.error(message, e)
    return { error: message }
  }
}

/** Port of `error-message`. */
function DataError({ message, darkMode }) {
  return (
    <Center h="100%" p="md">
      <Stack align="center" gap="xs" c={darkMode ? 'red.4' : 'red.7'}>
        <Group gap="xs" align="center">
          <TriangleAlert size={24} />
          <Text fw={500}>Data Error</Text>
        </Group>

        <Text ta="center">{message}</Text>

        <Text mt="md" size="sm" ta="center" c="dimmed">
          {
            'Check if the data contains tab characters (\\t) within values or if there are inconsistencies in the format.'
          }
        </Text>
      </Stack>
    </Center>
  )
}

/**
 * Table of SQL query results.
 *
 * @param {string[]} heads    column headers
 * @param {Array[]}  body     rows as a matrix, one array of cells per row
 * @param {boolean}  loading  renders a spinner instead of the grid
 * @param {boolean}  darkMode picks the dark colour scheme
 * @param {string}   height   height of the grid container
 * @param {string}   theme    base ag-grid theme: alpine | balham | material | quartz
 * @param {boolean}  pagination
 * @param {number}   pageSize rows per page when pagination is on
 * @param {boolean}  autoSizeColumns fit every column to its content once ready
 */
function AgGridTable({
  heads,
  body,
  loading = false,
  darkMode = false,
  height = '400px',
  theme = 'alpine',
  pagination = false,
  pageSize = 20,
  autoSizeColumns = true,
}) {
  const grid = useMemo(() => buildGrid(heads, body), [heads, body])
  const gridTheme = useMemo(() => resolveTheme(theme, darkMode), [theme, darkMode])

  const handleGridReady = useCallback(
    (params) => {
      if (autoSizeColumns) params.api.autoSizeAllColumns()
    },
    [autoSizeColumns]
  )

  if (loading) {
    return (
      <Center h="100%" w="100%">
        <Loader size="sm" color={darkMode ? 'gray.0' : 'gray.6'} />
      </Center>
    )
  }

  if (grid.empty) {
    return (
      <Center h="100%" w="100%">
        <Text c="dimmed">No results available</Text>
      </Center>
    )
  }

  if (grid.error) {
    return <DataError message={grid.error} darkMode={darkMode} />
  }

  return (
    <Box component="section" h={height} w="100%" aria-label="Query results table">
      <AgGridReact
        theme={gridTheme}
        columnDefs={grid.columns}
        rowData={grid.rowData}
        defaultColDef={DEFAULT_COL_DEF}
        pagination={pagination}
        paginationPageSize={pageSize}
        onGridReady={handleGridReady}
      />
    </Box>
  )
}

export default AgGridTable
