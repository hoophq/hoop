import { useState } from 'react'
import { Checkbox, Group, Stack, Text } from '@mantine/core'
import { CircleHelp, Info, SquarePen } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import RuleTableControls from '@/components/RuleTableControls'
import Select from '@/components/Select'
import Table from '@/components/Table'
import TagsInput from '@/components/TagsInput'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import Tooltip from '@/components/Tooltip'
import { makeRowOps } from '@/utils/rowOps'
import {
  CUSTOM_RULE,
  PRESETS,
  RULE_TYPE_OPTIONS,
  createEmptyRow,
  presetOptionsForType,
} from '../../helpers'

const MESSAGE_TOOLTIP =
  'When this rule is violated, this message will be displayed to the user along the Rule Name and Configuration'

// Row controls run one step below the app-wide md default (32px/14px instead
// of 40px/16px), which is the density the legacy table was designed at.
const ROW_CONTROL_SIZE = 'sm'

// Select columns are sized to their longest option plus the input's padding
// and chevron. They cannot size themselves: Mantine's Select is an <input>,
// whose intrinsic width is fixed, so a longer value scrolls out of view
// instead of widening the cell — where the legacy Radix trigger was a
// <button> that grew to fit its text, which is why porting the 160/220 widths
// straight over clipped "Pattern Match" and "Require WHERE clause (DELETE)".
// Revisit when adding an option longer than those.
const TYPE_COLUMN_WIDTH = 180
const RULE_COLUMN_WIDTH = 300

// The regex is kept in local state and committed on blur so the form is not
// churned on every keystroke. The cell is keyed by rule in the parent, so
// picking a different preset remounts it and re-seeds the draft.
function PatternCell({ row, onCommit }) {
  const [draft, setDraft] = useState(row.pattern_regex)

  return (
    <Group gap="xs" wrap="nowrap">
      <TextInput
        flex={1}
        size={ROW_CONTROL_SIZE}
        placeholder="Describe how this is used in your resource roles"
        value={draft}
        onChange={(event) => setDraft(event.currentTarget.value)}
        onBlur={() => onCommit(draft)}
        onKeyDown={(event) => {
          if (event.key === 'Enter') event.preventDefault()
        }}
        aria-label="Pattern"
      />
      <Tooltip label="Use Python regex syntax.">
        <CircleHelp size={16} />
      </Tooltip>
    </Group>
  )
}

// Collapsed by default (link or two-line preview) and expanded into a textarea
// on demand, mirroring the legacy cell. The draft is committed trimmed on blur.
function MessageCell({ row, onCommit }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(row.message)

  const startEditing = () => {
    setDraft(row.message)
    setEditing(true)
  }

  if (editing) {
    return (
      <Textarea
        autoFocus
        autosize={false}
        size={ROW_CONTROL_SIZE}
        rows={3}
        placeholder="Describe the message shown to the user when this rule is triggered"
        value={draft}
        onChange={(event) => setDraft(event.currentTarget.value)}
        onBlur={() => {
          setEditing(false)
          onCommit((draft ?? '').trim())
        }}
        aria-label="Custom error message"
      />
    )
  }

  return (
    <Group gap="xs" wrap="nowrap" justify="space-between">
      {row.message ? (
        <Text size="sm" flex={1} lineClamp={2}>
          {row.message}
        </Text>
      ) : (
        <Button variant="subtle" size="compact-xs" onClick={startEditing}>
          Set a custom error message
        </Button>
      )}
      <ActionIcon
        variant="subtle"
        color="gray"
        size={ROW_CONTROL_SIZE}
        onClick={startEditing}
        aria-label={
          row.message ? 'Edit custom error message' : 'Set a custom error message'
        }
      >
        <SquarePen size={16} />
      </ActionIcon>
    </Group>
  )
}

// One direction of a guardrail's rules. Rendered twice by the form, once for
// the input rules and once for the output rules.
export default function RulesTable({
  title,
  rules,
  setRules,
  selectMode,
  setSelectMode,
  freeLicense,
}) {
  const ops = makeRowOps({ rows: rules, setRows: setRules, factory: createEmptyRow })

  // The custom error message describes one specific configuration, so switching
  // the engine invalidates it. Switching presets within a type keeps it.
  const changeType = (row, value) =>
    ops.patchRow(row.id, {
      type: value || '',
      rule: '',
      pattern_regex: '',
      words: [],
      message: '',
    })

  const changeRule = (row, value) => {
    const preset = PRESETS[value]
    if (!preset) {
      ops.patchRow(row.id, { rule: value || '' })
      return
    }
    ops.patchRow(row.id, {
      rule: value,
      pattern_regex: preset.pattern_regex ?? '',
      words: preset.words ? [...preset.words] : [],
    })
  }

  const ruleOptions = (type) => {
    const customRule = { value: CUSTOM_RULE, label: 'Create custom rule' }
    const presets = presetOptionsForType(type)
    return presets.length
      ? [customRule, { group: 'Presets', items: presets }]
      : [customRule]
  }

  return (
    <Stack gap="md">
      <Text size="sm" fw={700}>
        {title}
      </Text>

      <Table>
        <Table.Thead>
          <Table.Tr>
            {selectMode && <Table.Th w={40} />}
            <Table.Th w={TYPE_COLUMN_WIDTH}>Type</Table.Th>
            <Table.Th w={RULE_COLUMN_WIDTH}>Rule</Table.Th>
            <Table.Th>Details</Table.Th>
            <Table.Th>
              <Group gap="xs" wrap="nowrap">
                {'Custom error message'}
                <Tooltip label={MESSAGE_TOOLTIP}>
                  <Info size={14} />
                </Tooltip>
              </Group>
            </Table.Th>
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
                  placeholder="Select one"
                  size={ROW_CONTROL_SIZE}
                  data={RULE_TYPE_OPTIONS}
                  value={row.type || null}
                  onChange={(value) => changeType(row, value)}
                  comboboxProps={{ withinPortal: true }}
                  aria-label="Rule type"
                />
              </Table.Td>
              <Table.Td>
                {row.type && (
                  <Select
                    placeholder="Select one"
                    size={ROW_CONTROL_SIZE}
                    data={ruleOptions(row.type)}
                    value={row.rule || null}
                    onChange={(value) => changeRule(row, value)}
                    comboboxProps={{ withinPortal: true }}
                    aria-label="Rule"
                  />
                )}
              </Table.Td>
              <Table.Td>
                {row.rule && row.type === 'pattern_match' && (
                  <PatternCell
                    key={`${row.id}:${row.rule}`}
                    row={row}
                    onCommit={(value) =>
                      ops.patchRow(row.id, { pattern_regex: value })
                    }
                  />
                )}
                {row.rule && row.type === 'deny_words_list' && (
                  <TagsInput
                    size={ROW_CONTROL_SIZE}
                    value={row.words}
                    onChange={(values) => ops.patchRow(row.id, { words: values })}
                    aria-label="Denied words"
                  />
                )}
              </Table.Td>
              <Table.Td>
                {row.rule && (
                  <MessageCell
                    row={row}
                    onCommit={(value) => ops.patchRow(row.id, { message: value })}
                  />
                )}
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
        disableNew={freeLicense && rules.length >= 1}
      />
    </Stack>
  )
}
