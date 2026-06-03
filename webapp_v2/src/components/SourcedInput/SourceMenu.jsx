import { Menu, Text, UnstyledButton } from '@mantine/core'
import { ChevronDown } from 'lucide-react'
import {
  SOURCE_ICONS,
  SOURCE_LABELS,
  SOURCES,
} from '@/pages/Roles/Configure/utils/secretsCodec'

// Picker dropdown used by both SourcedInput variants. The trigger is an
// UnstyledButton so each variant can apply its own outline + spacing
// styles via `targetClassName`. Items render an icon + label per source
// from SOURCE_ICONS / SOURCE_LABELS so the user always sees which
// provider a credential routes through.
//
// Behaves like a Select but renders via Menu — Mantine's Select doesn't
// allow custom icons in the trigger AND in each item without dropping
// to the Combobox primitive, which is heavier than this case warrants.
export default function SourceMenu({
  source,
  sources,
  onSourceChange,
  targetClassName,
  disabled,
}) {
  const options = Array.isArray(sources) ? sources : []
  const activeSource = source || options[0] || SOURCES.MANUAL
  const ActiveIcon = SOURCE_ICONS[activeSource]
  const activeLabel = SOURCE_LABELS[activeSource] || activeSource

  return (
    <Menu position="bottom-start" shadow="md" withinPortal disabled={disabled}>
      <Menu.Target>
        <UnstyledButton
          type="button"
          className={targetClassName}
          aria-label="Credential source"
          disabled={disabled}
        >
          {ActiveIcon && <ActiveIcon size={14} />}
          <Text size="sm" lh={1} fw={500}>
            {activeLabel}
          </Text>
          <ChevronDown size={14} />
        </UnstyledButton>
      </Menu.Target>
      <Menu.Dropdown>
        {options.map((s) => {
          const Icon = SOURCE_ICONS[s]
          return (
            <Menu.Item
              key={s}
              leftSection={Icon ? <Icon size={14} /> : null}
              onClick={() => onSourceChange?.(s)}
            >
              {SOURCE_LABELS[s] || s}
            </Menu.Item>
          )
        })}
      </Menu.Dropdown>
    </Menu>
  )
}
