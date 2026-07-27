import { Box, Checkbox, Group, Stack, Text } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import Badge from '@/components/Badge'
import Select from '@/components/Select'
import MultiSelect from '@/components/MultiSelect'
import TextInput from '@/components/TextInput'
import Table from '@/components/Table'
import {
  RULE_TYPES,
  PRESET_OPTIONS,
  FIELD_OPTIONS,
  getPresetValues,
  normalizeEntityName,
  createEmptyRow,
} from '../../helpers'

function RuleCell({ row, freeLicense, onChange }) {
  if (!row.type) return null

  if (row.type === 'presets') {
    const options = freeLicense
      ? PRESET_OPTIONS.map((opt) => ({
          ...opt,
          disabled: opt.value !== row.rule,
        }))
      : PRESET_OPTIONS
    return (
      <Select
        placeholder="Select preset"
        data={options}
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
        data={FIELD_OPTIONS}
        value={Array.isArray(row.details) ? row.details : []}
        onChange={(values) =>
          onChange({ details: freeLicense ? values.slice(-1) : values })
        }
        searchable
        comboboxProps={{ withinPortal: true }}
      />
    )
  }
  return (
    <TextInput
      placeholder="\b[A-Z]{2}[0-9]{3}\b"
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
            <Table.Th w={180}>Type</Table.Th>
            <Table.Th w={220}>Rule</Table.Th>
            <Table.Th>Details</Table.Th>
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
