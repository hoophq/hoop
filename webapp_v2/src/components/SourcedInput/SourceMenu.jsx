import { Menu, Text, UnstyledButton } from '@mantine/core'
import { ChevronDown } from 'lucide-react'
import { SOURCE_LABELS } from '@/pages/Roles/Configure/utils/secretsCodec'

// Picker dropdown used by both SourcedInput variants. The trigger is an
// UnstyledButton so each variant can apply its own outline / spacing
// styles via `targetClassName`. Just a label and a chevron — no icons,
// since the source name itself is what carries the meaning.
//
// Accepts either a plain string array (`['manual-input', '...']`) or
// the Mantine Select shape (`[{value, label}]`) so it works with the
// existing `sourceOptionsFor` helper unchanged.
export default function SourceMenu({
  source,
  sources,
  onSourceChange,
  targetClassName,
  disabled,
}) {
  const options = normalizeOptions(sources)
  const activeValue = source || options[0]?.value
  const activeOption = options.find((o) => o.value === activeValue) || options[0]
  const activeLabel = activeOption?.label || SOURCE_LABELS[activeValue] || activeValue

  return (
    <Menu position="bottom-start" shadow="md" withinPortal disabled={disabled}>
      <Menu.Target>
        <UnstyledButton
          type="button"
          className={targetClassName}
          aria-label="Credential source"
          disabled={disabled}
        >
          <Text size="sm" lh={1} fw={500} component="span">
            {activeLabel}
          </Text>
          <ChevronDown size={14} />
        </UnstyledButton>
      </Menu.Target>
      <Menu.Dropdown>
        {options.map((opt) => (
          <Menu.Item key={opt.value} onClick={() => onSourceChange?.(opt.value)}>
            {opt.label}
          </Menu.Item>
        ))}
      </Menu.Dropdown>
    </Menu>
  )
}

function normalizeOptions(sources) {
  if (!Array.isArray(sources)) return []
  return sources.map((s) =>
    typeof s === 'string'
      ? { value: s, label: SOURCE_LABELS[s] || s }
      : { value: s.value, label: s.label || SOURCE_LABELS[s.value] || s.value },
  )
}
