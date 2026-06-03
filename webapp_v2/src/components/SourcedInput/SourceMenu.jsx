import { Menu, Text, UnstyledButton } from '@mantine/core'
import { ChevronDown } from 'lucide-react'
import { SOURCE_LABELS } from '@/pages/Roles/Configure/utils/secretsCodec'

// Source picker dropdown. Accepts either a string array
// (`['manual-input', ...]`) or the Mantine Select shape
// (`[{value, label}]`) so it composes with the existing
// `sourceOptionsFor` helper unchanged.
export default function SourceMenu({
  source,
  sources,
  onSourceChange,
  targetClassName,
  targetSize,
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
          data-size={targetSize}
          aria-label="Credential source"
          disabled={disabled}
        >
          <Text lh={1} fw={500} component="span" inherit>
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
