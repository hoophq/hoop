import { Checkbox, Stack } from '@mantine/core'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import Table from '@/components/Table'
import RuleTableControls from '@/components/RuleTableControls'
import { makeRowOps } from '@/utils/rowOps'
import {
  CONNECTION_TAG_PREFIX,
  createEmptyMappingRow,
  isConnectionTagRule,
} from '../../helpers'

// Renders only the mapping rules bound to resource-role tags; the remaining
// rules of the same shared array are rendered by AutomatedMappingTable.
export default function PresetMappingTable({
  rows,
  setRows,
  selectMode,
  setSelectMode,
  tagOptions,
  freeLicense,
}) {
  const ops = makeRowOps({
    rows,
    setRows,
    factory: createEmptyMappingRow,
    filterFn: isConnectionTagRule,
  })

  const firstTagValue = tagOptions[0]?.value ?? CONNECTION_TAG_PREFIX
  const addPresetRule = () =>
    ops.addRow((row) => ({ ...row, type: 'preset', value: firstTagValue }))

  return (
    <Stack gap="md">
      <Table>
        <Table.Thead>
          <Table.Tr>
            {selectMode && <Table.Th w={40} />}
            <Table.Th>Tag</Table.Th>
            <Table.Th>Jira Field</Table.Th>
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
                  placeholder="Select tag"
                  data={tagOptions}
                  value={row.value || null}
                  onChange={(v) => ops.patchRow(row.id, { value: v || '' })}
                  comboboxProps={{ withinPortal: true }}
                />
              </Table.Td>
              <Table.Td>
                <TextInput
                  placeholder="e.g. customfield_0410"
                  value={row.jira_field}
                  onChange={(e) =>
                    ops.patchRow(row.id, { jira_field: e.currentTarget.value })
                  }
                />
              </Table.Td>
              <Table.Td>
                <TextInput
                  placeholder="e.g. Environment"
                  value={row.description}
                  onChange={(e) =>
                    ops.patchRow(row.id, { description: e.currentTarget.value })
                  }
                />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>

      <RuleTableControls
        onAdd={addPresetRule}
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
