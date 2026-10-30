import { Fragment, useState } from 'react'
import { Box, Text } from '@mantine/core'
import { AlertCircle, ChevronDown, ChevronRight } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Checkbox from '@/components/Checkbox'
import Table from '@/components/Table'
import classes from './TreeTable.module.css'

const INDENT = 20

const hasError = (rows) => rows.some((row) => row.error || hasError(row.children ?? []))

/**
 * Rows with children and an error message that expand under their row, and an
 * optional checkbox column. Port of webapp.components.data-table-simple.
 *
 * columns:   [{ id, header, width, render(row) }]
 * rows:      [{ id, name, children?, error? }]; `error` is a message.
 * selection: { checked(row) -> true | false | 'some', selectable(row), onChange(row, checked),
 *              all: true | false | 'some', onChangeAll(checked), allDisabled }
 * expanded, onToggleExpand: controlled expansion; without them the table keeps its own,
 *            starting from defaultExpanded.
 */
export default function TreeTable({ columns, rows, empty, selection, expanded, onToggleExpand, defaultExpanded }) {
  const [ownExpanded, setOwnExpanded] = useState(defaultExpanded ?? [])
  const open = expanded ?? ownExpanded
  const toggle =
    onToggleExpand ?? ((id) => setOwnExpanded((ids) => (ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id])))
  const errors = hasError(rows)
  const span = columns.length + 1 + (selection ? 1 : 0) + (errors ? 1 : 0)

  const renderRows = (list, level) =>
    list.map((row) => {
      const hasChildren = row.children?.length > 0
      const isOpen = open.includes(row.id)
      const checked = selection ? selection.checked(row) : false
      return (
        <Fragment key={row.id}>
          <Table.Tr bg={row.error ? 'red.0' : checked === true ? 'indigo.0' : undefined}>
            {selection && (
              <Table.Td w={40}>
                {selection.selectable(row) && (
                  <Checkbox
                    aria-label={`Select ${row.name}`}
                    checked={checked === true}
                    indeterminate={checked === 'some'}
                    onChange={(e) => selection.onChange(row, e.currentTarget.checked)}
                  />
                )}
              </Table.Td>
            )}
            <Table.Td w={40}>
              {(hasChildren || row.error) && (
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  size="sm"
                  aria-label={`${isOpen ? 'Collapse' : 'Expand'} ${row.name}`}
                  aria-expanded={isOpen}
                  onClick={() => toggle(row.id)}
                >
                  {isOpen ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                </ActionIcon>
              )}
            </Table.Td>
            {columns.map((column, index) => (
              <Table.Td key={column.id} w={column.width}>
                {index === 0 ? (
                  <Box pl={level * INDENT} className={classes.name}>
                    {column.render ? column.render(row) : row[column.id]}
                  </Box>
                ) : column.render ? (
                  column.render(row)
                ) : (
                  row[column.id]
                )}
              </Table.Td>
            ))}
            {errors && (
              <Table.Td w={40}>
                {row.error && <AlertCircle size={16} color="var(--mantine-color-red-6)" aria-label="Error" />}
              </Table.Td>
            )}
          </Table.Tr>
          {row.error && isOpen && (
            <Table.Tr>
              <Table.Td colSpan={span}>
                <Text size="sm" c="red.8">
                  {row.error}
                </Text>
              </Table.Td>
            </Table.Tr>
          )}
          {hasChildren && isOpen && renderRows(row.children, level + 1)}
        </Fragment>
      )
    })

  return (
    <Box w="100%">
      <Table scrollable>
        <Table.Thead>
          <Table.Tr>
            {selection && (
              <Table.Th w={40}>
                <Checkbox
                  aria-label="Select all"
                  checked={selection.all === true}
                  indeterminate={selection.all === 'some'}
                  disabled={selection.allDisabled || rows.length === 0}
                  onChange={(e) => selection.onChangeAll(e.currentTarget.checked)}
                />
              </Table.Th>
            )}
            <Table.Th w={40} />
            {columns.map((column) => (
              <Table.Th key={column.id} w={column.width}>
                {column.header}
              </Table.Th>
            ))}
            {errors && <Table.Th w={40} />}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.length === 0 ? (
            <Table.Tr>
              <Table.Td colSpan={span}>
                <Text size="sm" c="dimmed" ta="center" py="md">
                  {empty}
                </Text>
              </Table.Td>
            </Table.Tr>
          ) : (
            renderRows(rows, 0)
          )}
        </Table.Tbody>
      </Table>
    </Box>
  )
}
