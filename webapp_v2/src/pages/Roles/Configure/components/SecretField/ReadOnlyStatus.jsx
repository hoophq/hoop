import { Group, Stack, ThemeIcon } from '@mantine/core'
import { Check } from 'lucide-react'
import TextInput from '@/components/TextInput'
import Textarea from '@/components/Textarea'
import FieldLabel from './FieldLabel'
import InlineAction from './InlineAction'
import { SECRET_MASK } from './util'

const RIGHT_SECTION_WIDTH = 112

// The "set" state of SecretField: an existing inline secret. The value is
// never shown — a masked input with a Replace action stands in for it.
// Provider references (Vault / AWS) render their reference text verbatim,
// since it's a safe, useful pointer rather than the secret itself.
export default function ReadOnlyStatus({
  label,
  required,
  type,
  isReference,
  referenceText,
  onReplace,
}) {
  const replaceAction = <InlineAction kind="replace" onClick={onReplace} />

  // Textareas put the action in a header row — Mantine input sections sit
  // centered, which looks wrong against a tall masked block.
  if (type === 'textarea') {
    return (
      <Stack gap={4}>
        <Group justify="space-between" align="center">
          <FieldLabel label={label} required={required} />
          {replaceAction}
        </Group>
        <Textarea
          readOnly
          value={[SECRET_MASK, SECRET_MASK, SECRET_MASK].join('\n')}
        />
      </Stack>
    )
  }

  return (
    <TextInput
      label={label}
      withAsterisk={required}
      readOnly
      value={isReference ? referenceText : SECRET_MASK}
      leftSection={
        <ThemeIcon
          size="sm"
          radius="xl"
          variant="light"
          color={isReference ? 'indigo' : 'green'}
        >
          <Check size={12} />
        </ThemeIcon>
      }
      rightSection={replaceAction}
      rightSectionWidth={RIGHT_SECTION_WIDTH}
      rightSectionPointerEvents="auto"
    />
  )
}
