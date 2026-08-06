import { Checkbox, Stack } from '@mantine/core'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import Table from '@/components/Table'
import RuleTableControls from '@/components/RuleTableControls'
import { makeRowOps } from '@/utils/rowOps'
import { REQUIRED_OPTIONS, createEmptyCmdbRow } from '../../helpers'
import CmdbAssetPicker from './CmdbAssetPicker'

export default function CmdbTable({
  rows,
  setRows,
  selectMode,
  setSelectMode,
  freeLicense,
}) {
  const ops = makeRowOps({ rows, setRows, factory: createEmptyCmdbRow })

  return (
    <Stack gap="md">
      <Table>
        <Table.Thead>
          <Table.Tr>
            {selectMode && <Table.Th w={40} />}
            <Table.Th>Label</Table.Th>
            <Table.Th>Jira Field</Table.Th>
            <Table.Th>Value</Table.Th>
            <Table.Th>Object Schema ID (Optional)</Table.Th>
            <Table.Th>Object Type ID</Table.Th>
            <Table.Th>Description (Optional)</Table.Th>
            <Table.Th w={100}>Required</Table.Th>
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
                <TextInput
                  placeholder="e.g. Employee ID"
                  value={row.label}
                  onChange={(e) =>
                    ops.patchRow(row.id, { label: e.currentTarget.value })
                  }
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
                {row.jira_object_type ? (
                  <CmdbAssetPicker
                    row={row}
                    onPatch={(patch) => ops.patchRow(row.id, patch)}
                  />
                ) : (
                  <TextInput
                    placeholder="e.g. value_123"
                    value={row.value}
                    onChange={(e) =>
                      ops.patchRow(row.id, { value: e.currentTarget.value })
                    }
                  />
                )}
              </Table.Td>
              <Table.Td>
                <TextInput
                  placeholder="e.g. 9"
                  value={row.jira_object_schema_id}
                  onChange={(e) =>
                    ops.patchRow(row.id, {
                      jira_object_schema_id: e.currentTarget.value,
                    })
                  }
                />
              </Table.Td>
              <Table.Td>
                <TextInput
                  placeholder="e.g. 13"
                  value={row.jira_object_type}
                  onChange={(e) =>
                    ops.patchRow(row.id, { jira_object_type: e.currentTarget.value })
                  }
                />
              </Table.Td>
              <Table.Td>
                <TextInput
                  placeholder="e.g. customfield_0410"
                  value={row.description}
                  onChange={(e) =>
                    ops.patchRow(row.id, { description: e.currentTarget.value })
                  }
                />
              </Table.Td>
              <Table.Td>
                <Select
                  data={REQUIRED_OPTIONS}
                  value={String(row.required)}
                  onChange={(v) => ops.patchRow(row.id, { required: v === 'true' })}
                  comboboxProps={{ withinPortal: true }}
                  aria-label="Required"
                />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>

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
