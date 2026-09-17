import { Box, Checkbox, Group, Stack, Text } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import Badge from '@/components/Badge'
import Select from '@/components/Select'
import MultiSelect from '@/components/MultiSelect'
import TextInput from '@/components/TextInput'
import TagsInput from '@/components/TagsInput'
import Table from '@/components/Table'
import {
  RULE_TYPES,
  PRESET_OPTIONS,
  FIELD_OPTIONS,
  getPresetValues,
  normalizeEntityName,
  createEmptyRow,
} from '../../helpers'

// Row controls run one step below the app-wide md default (32px/14px instead
// of 40px/16px), which is the density the legacy table was designed at.
const ROW_CONTROL_SIZE = 'sm'

// Select columns are sized to their longest option plus the input's padding
// and chevron. They cannot size themselves: Mantine's Select is an <input>,
// whose intrinsic width is fixed, so a longer value scrolls out of view
// instead of widening the cell. Type holds "Presets"/"Fields"/"Custom"; Rule
// holds the preset names, the longest being "Cryptocurrency Identifiers".
const TYPE_COLUMN_WIDTH = 180
const RULE_COLUMN_WIDTH = 280

function RuleCell({ row, freeLicense, onChange }) {
  if (!row.type) return null

  if (row.type === 'presets') {
    return (
      <Select
        placeholder="Select preset"
        size={ROW_CONTROL_SIZE}
        data={PRESET_OPTIONS}
        value={row.rule || null}
        onChange={(v) => onChange({ rule: v || '' })}
        comboboxProps={{ withinPortal: true }}
      />
    )
  }

  if (row.type === 'fields') {
    return <Text size="sm">Custom Selection</Text>
  }
  return (
    <TextInput
      placeholder="Rule Name"
      size={ROW_CONTROL_SIZE}
      value={row.rule}
      onChange={(e) => onChange({ rule: e.currentTarget.value })}
      onBlur={(e) => {
        const normalized = normalizeEntityName(e.currentTarget.value)
        if (normalized !== e.currentTarget.value) onChange({ rule: normalized })
      }}
    />
  )
}

function DetailsCell({ row, freeLicense, onChange }) {
  if (!row.type) return null

  if (row.type === 'presets') {
    const values = getPresetValues(row.rule)
    return (
      <Group gap={4} wrap="wrap">
        {values.map((value) => (
          <Badge key={value} color="gray" variant="filled">
            {value}
          </Badge>
        ))}
      </Group>
    )
  }

  if (row.type === 'fields') {
    return (
      <MultiSelect
        placeholder="Select rules..."
        size={ROW_CONTROL_SIZE}
        data={FIELD_OPTIONS}
        value={Array.isArray(row.details) ? row.details : []}
        onChange={(values) => onChange({ details: values })}
        searchable
        comboboxProps={{ withinPortal: true }}
      />
    )
  }
  return (
    <TextInput
      placeholder="\b[A-Z]{2}[0-9]{3}\b"
      size={ROW_CONTROL_SIZE}
      value={row.details}
      onChange={(e) => onChange({ details: e.currentTarget.value })}
    />
  )
}

export default function RulesTable({
  rules,
  setRules,
  selectMode,
  setSelectMode,
  freeLicense,
}) {
  const allSelected = rules.length > 0 && rules.every((r) => r.selected)

  const patchRow = (idx, patch) =>
    setRules((rows) => rows.map((r, i) => (i === idx ? { ...r, ...patch } : r)))

  const changeType = (idx, type) => {
    const reset =
      type === 'fields'
        ? { type, rule: 'Custom Selection', details: [] }
        : { type, rule: '', details: '' }
    patchRow(idx, reset)
  }

  const toggleSelect = (idx) =>
    setRules((rows) =>
      rows.map((r, i) => (i === idx ? { ...r, selected: !r.selected } : r)),
    )

  const toggleAll = () =>
    setRules((rows) => rows.map((r) => ({ ...r, selected: !allSelected })))

  const deleteSelected = () =>
    setRules((rows) => {
      const remaining = rows.filter((r) => !r.selected)
      return remaining.length ? remaining : [createEmptyRow()]
    })

  const addRow = () => setRules((rows) => [...rows, createEmptyRow()])

  return (
    <Stack gap="md">
      <Table>
        <Table.Thead>
          <Table.Tr>
            {selectMode && <Table.Th w={40} />}
            <Table.Th w={TYPE_COLUMN_WIDTH}>Type</Table.Th>
            <Table.Th w={RULE_COLUMN_WIDTH}>Rule Name</Table.Th>
            <Table.Th>Entities</Table.Th>
            <Table.Th w={150}>Strategy</Table.Th>
            <Table.Th w={200}>Parameters / Columns</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rules.map((row, idx) => (
            <Table.Tr key={row.id}>
              {selectMode && (
                <Table.Td>
                  <Checkbox
                    checked={!!row.selected}
                    onChange={() => toggleSelect(idx)}
                    aria-label="Select rule"
                  />
                </Table.Td>
              )}
              <Table.Td>
                <Select
                  placeholder="Select type"
                  size={ROW_CONTROL_SIZE}
                  data={RULE_TYPES}
                  value={row.type || null}
                  onChange={(v) => changeType(idx, v || '')}
                  comboboxProps={{ withinPortal: true }}
                />
              </Table.Td>
              <Table.Td>
                <RuleCell
                  row={row}
                  freeLicense={freeLicense}
                  onChange={(patch) => patchRow(idx, patch)}
                />
              </Table.Td>
              <Table.Td>
                <DetailsCell
                  row={row}
                  freeLicense={freeLicense}
                  onChange={(patch) => patchRow(idx, patch)}
                />
              </Table.Td>
              <Table.Td>
                {row.type && (
                  <Select
                    size={ROW_CONTROL_SIZE}
                    data={[
                      { value: 'redact', label: 'Redact' },
                      { value: 'mask', label: 'Mask' },
                      { value: 'partial', label: 'Partial' },
                      { value: 'hash', label: 'Hash' },
                    ]}
                    value={row.strategy || 'redact'}
                    onChange={(v) => patchRow(idx, { strategy: v || 'redact' })}
                    comboboxProps={{ withinPortal: true }}
                  />
                )}
              </Table.Td>
              <Table.Td>
                {row.type && (
                  <Stack gap="xs">
                    {row.strategy === 'partial' && (
                      <TextInput
                        type="number"
                        min={0}
                        placeholder="Keep Last (4)"
                        size={ROW_CONTROL_SIZE}
                        value={row.keepLast ?? 4}
                        onChange={(e) => patchRow(idx, { keepLast: Number(e.currentTarget.value) })}
                      />
                    )}
                    {(row.strategy === 'mask' || row.strategy === 'partial') && (
                      <TextInput
                        maxLength={1}
                        placeholder="Mask Char (*)"
                        size={ROW_CONTROL_SIZE}
                        value={row.maskChar ?? '*'}
                        onChange={(e) => patchRow(idx, { maskChar: e.currentTarget.value })}
                      />
                    )}
                    <TagsInput
                      placeholder="Columns to mask"
                      size={ROW_CONTROL_SIZE}
                      value={row.columns ?? []}
                      onChange={(values) => patchRow(idx, { columns: values })}
                    />
                  </Stack>
                )}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>

      {!freeLicense && (
        <Group gap="sm">
          <Button
            type="button"
            variant="light"
            leftSection={<Plus size={14} />}
            onClick={addRow}
          >
            New
          </Button>
          <Button
            type="button"
            variant="light"
            color="gray"
            onClick={() => setSelectMode((v) => !v)}
          >
            Select
          </Button>
          {selectMode && (
            <>
              <Button
                type="button"
                variant="light"
                color="gray"
                onClick={toggleAll}
              >
                {allSelected ? 'Unselect all' : 'Select all'}
              </Button>
              <Button
                type="button"
                variant="light"
                color="red"
                leftSection={<Trash2 size={14} />}
                onClick={deleteSelected}
              >
                Delete
              </Button>
            </>
          )}
        </Group>
      )}
    </Stack>
  )
}
