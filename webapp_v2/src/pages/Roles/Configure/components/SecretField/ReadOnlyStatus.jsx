import { ThemeIcon } from '@mantine/core'
import { Check } from 'lucide-react'
import TextInput from '@/components/TextInput'
import Textarea from '@/components/Textarea'
import InlineAction from './InlineAction'
import { SECRET_MASK } from './util'
import classes from './SecretField.module.css'

const RIGHT_SECTION_WIDTH = 112

// The "set" state of SecretField: an existing inline secret. The value is
// never shown — a masked input with a Replace action stands in for it.
// Provider references (Vault / AWS) render their reference text verbatim,
// since it's a safe, useful pointer rather than the secret itself. Textareas
// share the same in-border layout; the leading icon and action are
// top-aligned (via classes.topSection) instead of vertically centered.
export default function ReadOnlyStatus({
  label,
  required,
  type,
  isReference,
  referenceText,
  onReplace,
}) {
  const isTextarea = type === 'textarea'
  const Input = isTextarea ? Textarea : TextInput
  const value = isReference
    ? referenceText
    : isTextarea
      ? [SECRET_MASK, SECRET_MASK, SECRET_MASK].join('\n')
      : SECRET_MASK

  return (
    <Input
      label={label}
      withAsterisk={required}
      readOnly
      value={value}
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
      rightSection={<InlineAction kind="replace" onClick={onReplace} />}
      rightSectionWidth={RIGHT_SECTION_WIDTH}
      rightSectionPointerEvents="auto"
      classNames={isTextarea ? { section: classes.topSection } : undefined}
    />
  )
}
