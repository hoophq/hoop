import { Checkbox, Stack, Text } from '@mantine/core'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import Table from '@/components/Table'
import RuleTableControls from '@/components/RuleTableControls'
import { makeRowOps } from '@/utils/rowOps'
import {
  HOOP_VALUE_OPTIONS,
  MAPPING_TYPE_OPTIONS,
  createEmptyMappingRow,
  isNotConnectionTagRule,
} from '../../helpers'

function ValueCell({ row, onPatch }) {
  if (!row.type) return null
  if (row.type === 'preset') {
    return (
      <Select
        placeholder="Select value"
        data={HOOP_VALUE_OPTIONS}
        value={row.value || null}
        onChange={(v) => onPatch({ value: v || '' })}
        comboboxProps={{ withinPortal: true }}
      />
    )
  }
  return (
    <TextInput
      placeholder="e.g. product"
      value={row.value}
      onChange={(e) => onPatch({ value: e.currentTarget.value })}
    />
  )
}

// Renders the mapping rules NOT bound to resource-role tags; shares its rows
// array with PresetMappingTable.
export default function AutomatedMappingTable({
  rows,
  setRows,
  selectMode,
  setSelectMode,
  freeLicense,
}) {
  const ops = makeRowOps({
    rows,
    setRows,
    factory: createEmptyMappingRow,
    filterFn: isNotConnectionTagRule,
  })

  return (
    <Stack gap="md">
      <Stack gap="xs">
        <Table>
          <Table.Thead>
            <Table.Tr>
              {selectMode && <Table.Th w={40} />}
              <Table.Th w={160}>Type</Table.Th>
              <Table.Th>Jira Field</Table.Th>
              <Table.Th>Value</Table.Th>
              <Table.Th>Description (Optional)</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {ops.visible.map((row) => (
              <Table.Tr key={row.id}>
                {selectMode && (
                  <Table.Td>
                    <Checkbox
                      checked={!!row.selected}
                      onChange={() => ops.toggleSelect(row.id)}
                      aria-label="Select rule"
                    />
                  </Table.Td>
                )}
                <Table.Td>
                  <Select
                    placeholder="Select type"
                    data={MAPPING_TYPE_OPTIONS}
                    value={row.type || null}
                    onChange={(v) =>
                      ops.patchRow(row.id, {
                        type: v || '',
                        value: '',
                        jira_field: '',
                        description: '',
                      })
                    }
                    comboboxProps={{ withinPortal: true }}
                  />
                </Table.Td>
                <Table.Td>
                  {row.type && (
                    <TextInput
                      placeholder="e.g. customfield_0410"
                      value={row.jira_field}
                      onChange={(e) =>
                        ops.patchRow(row.id, { jira_field: e.currentTarget.value })
                      }
                    />
                  )}
                </Table.Td>
                <Table.Td>
                  <ValueCell
                    row={row}
                    onPatch={(patch) => ops.patchRow(row.id, patch)}
                  />
                </Table.Td>
                <Table.Td>
                  {row.type && (
                    <TextInput
                      placeholder="e.g. customfield_0410"
                      value={row.description}
                      onChange={(e) =>
                        ops.patchRow(row.id, {
                          description: e.currentTarget.value,
                        })
                      }
                    />
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        <Text size="sm" c="dimmed">
          <Text component="span" size="sm" fw={700}>
            {'Preset: '}
          </Text>
          {'Relates hoop.dev fields with Jira fields. '}
          <Text component="span" size="sm" fw={700}>
            {'Custom: '}
          </Text>
          {'Appends a custom key-value relation to Jira cards.'}
        </Text>
      </Stack>

      <RuleTableControls
        onAdd={() => ops.addRow()}
        selectMode={selectMode}
        onToggleSelectMode={() => setSelectMode((v) => !v)}
        allSelected={ops.allSelected}
        onToggleAll={ops.toggleAll}
        onDelete={ops.deleteSelected}
        disableNew={freeLicense && ops.visible.length >= 1}
      />
    </Stack>
  )
}
