import { Group, Input, Paper, Stack } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import MultiSelect from '@/components/MultiSelect'
import TagsInput from '@/components/TagsInput'

// One list of trigger conditions, `any` or `exclude`. Every field an item names
// must match (ADR-0030). A field this lane's form does not show, such as
// tables on an http lane, is kept as stored.
export default function TriggerItemsInput({ label, description, addLabel, value, onChange, operations, isHTTP }) {
  const setItem = (i, patch) => onChange(value.map((item, j) => (j === i ? { ...item, ...patch } : item)))

  return (
    <Input.Wrapper label={label} description={description}>
      <Stack gap="xs" mt={4}>
        {value.map((item, i) => (
          <Paper key={i} withBorder p="sm" radius="md">
            <Group gap="xs" align="flex-start" wrap="nowrap">
              <Stack gap="xs" flex={1}>
                <MultiSelect
                  label="Operations"
                  placeholder="Any operation"
                  data={operations}
                  value={item.operations ?? []}
                  onChange={(v) => setItem(i, { operations: v })}
                  searchable
                  clearable
                />
                {isHTTP ? (
                  <TagsInput
                    label="Resources"
                    placeholder="/orders/**"
                    value={item.resources ?? []}
                    onChange={(v) => setItem(i, { resources: v })}
                  />
                ) : (
                  <TagsInput
                    label="Tables"
                    placeholder="customers"
                    value={item.tables ?? []}
                    onChange={(v) => setItem(i, { tables: v })}
                  />
                )}
              </Stack>
              <ActionIcon
                variant="subtle"
                color="gray"
                aria-label="Remove condition"
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                <Trash2 size={16} />
              </ActionIcon>
            </Group>
          </Paper>
        ))}
        <Button
          variant="subtle"
          size="compact-sm"
          w="fit-content"
          leftSection={<Plus size={14} />}
          onClick={() => onChange([...value, {}])}
        >
          {addLabel}
        </Button>
      </Stack>
    </Input.Wrapper>
  )
}
