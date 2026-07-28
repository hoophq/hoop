import { Table as MantineTable, Box } from '@mantine/core'
import classes from './Table.module.css'

/**
 * Surface-style table — matches Radix Table.Root variant="surface" used in the legacy webapp.
 * Re-exports all sub-components so call sites never import from Mantine directly.
 *
 * Light chrome by design: gray-1 outer/row borders, gray-0 header, and
 * gray-0 striped rows (on by default — pass striped={false} to opt out).
 *
 * Usage:
 *   import Table from '@/components/Table'
 *   <Table>
 *     <Table.Thead>...</Table.Thead>
 *     <Table.Tbody>...</Table.Tbody>
 *   </Table>
 */
function Table({ children, ...props }) {
  return (
    <Box className={classes.surface}>
      <MantineTable
        withRowBorders
        stripedColor="rgba(240, 240, 243, .4)"
        borderColor="gray.2"
        verticalSpacing="sm"
        horizontalSpacing="md"
        classNames={{ thead: classes.thead, th: classes.th }}
        {...props}
      >
        {children}
      </MantineTable>
    </Box>
  )
}

Table.Thead = MantineTable.Thead
Table.Tbody = MantineTable.Tbody
Table.Tfoot = MantineTable.Tfoot
Table.Tr = MantineTable.Tr
Table.Th = MantineTable.Th
Table.Td = MantineTable.Td
Table.Caption = MantineTable.Caption

export default Table
