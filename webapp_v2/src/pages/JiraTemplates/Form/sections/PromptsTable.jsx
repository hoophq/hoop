import { Checkbox, Stack } from '@mantine/core'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import TagsInput from '@/components/TagsInput'
import Table from '@/components/Table'
import {
  FIELD_TYPE_OPTIONS,
  REQUIRED_OPTIONS,
  createEmptyPromptRow,
  makeRowOps,
} from '../../helpers'
import RuleTableControls from '../components/RuleTableControls'

export default function PromptsTable({
  rows,
  setRows,
  selectMode,
  setSelectMode,
  freeLicense,
}) {
  const ops = makeRowOps({ rows, setRows, factory: createEmptyPromptRow })

  return (
    <Stack gap="md">
      <Table>
        <Table.Thead>
          <Table.Tr>
            {selectMode && <Table.Th w={40} />}
            <Table.Th>Label</Table.Th>
            <Table.Th>Jira Field</Table.Th>
            <Table.Th>Type</Table.Th>
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
              <Table.Td w={row.field_type === 'select' ? 256 : undefined}>
                <Stack gap="xs">
                  <Select
                    placeholder="Select type"
                    data={FIELD_TYPE_OPTIONS}
                    value={row.field_type || null}
                    onChange={(v) => ops.patchRow(row.id, { field_type: v || '' })}
                    comboboxProps={{ withinPortal: true }}
                  />
                  {row.field_type === 'select' && (
                    <TagsInput
                      placeholder="Type an option and press Enter"
                      value={row.field_options}
                      onChange={(values) =>
                        ops.patchRow(row.id, { field_options: values })
                      }
                      aria-label="Select options"
                    />
                  )}
                </Stack>
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
