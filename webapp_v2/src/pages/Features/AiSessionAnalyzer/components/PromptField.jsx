import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import { SquarePen, Undo2 } from 'lucide-react'
import Button from '@/components/Button'
import Textarea from '@/components/Textarea'
import OptionalLabel from './OptionalLabel'

// An optional agent prompt: "Uses the default prompt." with "Edit prompt"
// until the user writes one; "Use default" clears it and closes the field.
export default function PromptField({ prompt, onChange, placeholder, helper }) {
  const [editing, setEditing] = useState(prompt !== '')
  const open = editing || prompt !== ''
  const useDefault = () => {
    onChange('')
    setEditing(false)
  }

  return (
    <Stack gap={6}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap={2}>
          <Text size="sm">
            <OptionalLabel>Your prompt</OptionalLabel>
          </Text>
          {!open && (
            <Text size="xs" c="dimmed">
              Uses the default prompt.
            </Text>
          )}
        </Stack>
        {open ? (
          <Button variant="transparent" size="xs" px={0} leftSection={<Undo2 size={14} />} onClick={useDefault}>
            Use default
          </Button>
        ) : (
          <Button variant="transparent" size="xs" px={0} leftSection={<SquarePen size={14} />} onClick={() => setEditing(true)}>
            Edit prompt
          </Button>
        )}
      </Group>
      {open && (
        <Textarea
          aria-label="Your prompt"
          placeholder={placeholder}
          description={helper}
          inputWrapperOrder={['label', 'input', 'description', 'error']}
          minRows={4}
          maxRows={10}
          autoFocus={editing && prompt === ''}
          value={prompt}
          onChange={(e) => onChange(e.currentTarget.value)}
        />
      )}
    </Stack>
  )
}
