import { Stack, Group } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import ActionIcon from '@/components/ActionIcon'
import TextInput from '@/components/TextInput'
import { useConfigureRoleStore } from '../store'

// Dynamic list of "field name" inputs for mandatory metadata. Sessions
// created against this role require the user to fill in each named
// field before executing a command.
//
// Always renders at least one row so the user has somewhere to type
// without having to click Add first.
export default function MetadataFieldsInput() {
  const fields = useConfigureRoleStore((s) => s.drafts.mandatory_metadata_fields)
  const setField = useConfigureRoleStore((s) => s.setMandatoryMetadataField)
  const addField = useConfigureRoleStore((s) => s.addMandatoryMetadataField)
  const removeField = useConfigureRoleStore((s) => s.removeMandatoryMetadataField)

  const rows = fields.length > 0 ? fields : ['']

  return (
    <Stack gap="sm">
      {rows.map((value, i) => (
        <Group key={i} gap="sm" wrap="nowrap" align="flex-end">
          <TextInput
            label={i === 0 ? 'Field Name' : undefined}
            placeholder="e.g. Ticket Number"
            value={value}
            onChange={(e) => setField(i, e.currentTarget.value)}
            flex={1}
          />
          {i > 0 && (
            <ActionIcon
              variant="subtle"
              color="red"
              size="lg"
              onClick={() => removeField(i)}
              aria-label="Remove field"
            >
              <Trash2 size={16} />
            </ActionIcon>
          )}
        </Group>
      ))}
      <Button
        variant="light"
        leftSection={<Plus size={14} />}
        w="fit-content"
        onClick={addField}
      >
        Add New Field
      </Button>
    </Stack>
  )
}
