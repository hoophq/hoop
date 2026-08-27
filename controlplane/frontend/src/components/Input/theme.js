import {
  Input,
  InputBase,
  Textarea,
  MultiSelect,
  TagsInput,
  PillsInput,
  rem,
} from '@mantine/core'
import classes from './Input.module.css'

// Global extension for every Input-based component (TextInput, Select,
// Textarea, MultiSelect, PasswordInput, NumberInput, TagsInput,
// Autocomplete, DatePickerInput, ...). All of them render through Input,
// so this file is the one place that controls form fields app-wide:
//
// - classNames.wrapper — resting border color of every field.
// - size — app-wide field height scale: xs=24px, sm=32px, md=40px
//   (default, no size prop needed at call sites), lg=48px. Heights come
//   from the global --hoop-control-height-* variables (src/theme.js
//   cssVariablesResolver), shared with Button and ActionIcon. Text pairs
//   with height (24↔12px, 32↔14, 40↔16, 48↔18). Mantine's stock presets
//   (sm=36, md=42, lg=50) are bypassed for these four sizes.
//
// IMPORTANT — the md default cannot live on Input alone. Several Mantine
// components hardcode a LOCAL `size: 'sm'` default and pass the resolved
// value down to Input as an explicit prop, which beats any theme default on
// the Input key. Theme defaultProps DO override those local defaults, so
// every such component gets its own entry below (registered in
// src/theme.js): InputBase (under TextInput, PasswordInput, Select,
// Autocomplete, NumberInput), Textarea, MultiSelect, TagsInput, PillsInput,
// and PickerInputBase from @mantine/dates (under DatePickerInput — plain
// object entry in src/theme.js since the component isn't exported).
// If you adopt another Mantine input with its own local size default
// (FileInput, JsonInput, ColorInput, PinInput, ...), add it here too.
const FONT_SIZES = {
  xs: 'var(--mantine-font-size-xs)',
  sm: 'var(--mantine-font-size-sm)',
  md: 'var(--mantine-font-size-md)',
  lg: 'var(--mantine-font-size-lg)',
}

const SIZE_DEFAULT = { size: 'md' }

export const InputTheme = Input.extend({
  classNames: { wrapper: classes.wrapper },
  defaultProps: SIZE_DEFAULT,
  vars: (_theme, props) => {
    const fz = FONT_SIZES[props.size]
    if (!fz) return { wrapper: {} }
    return {
      wrapper: {
        '--input-height': `var(--hoop-control-height-${props.size})`,
        '--input-fz': fz,
        // Multiline inputs (MultiSelect, TagsInput, PillsInput, Textarea)
        // are content-driven: height = inner row + 2×padding-y + borders,
        // and Mantine's multiline padding presets assume its taller stock
        // fields (a default MultiSelect lands at 38.4px instead of 32px).
        // The inner PillsInput field is always 1.6em tall, so the exact
        // padding for our scale is computable — 1em here is --input-fz
        // because the variable is consumed on the input element itself.
        // Chips fit the row because the Pill theme caps them at 22px
        // (see components/Pill/theme.js).
        '--input-padding-y': props.multiline
          ? `calc((var(--hoop-control-height-${props.size}) - ${rem(2)} - 1.6em) / 2)`
          : undefined,
      },
    }
  },
})

export const InputBaseTheme = InputBase.extend({ defaultProps: SIZE_DEFAULT })
export const TextareaTheme = Textarea.extend({ defaultProps: SIZE_DEFAULT })
export const MultiSelectTheme = MultiSelect.extend({ defaultProps: SIZE_DEFAULT })
export const TagsInputTheme = TagsInput.extend({ defaultProps: SIZE_DEFAULT })
export const PillsInputTheme = PillsInput.extend({ defaultProps: SIZE_DEFAULT })

// Descriptions and errors track the field size; labels are pinned to 14px/700
// to match the section headings they sit under. Four Create forms each carried
// a private CSS-module copy of this rule before it moved here.
export const InputWrapperTheme = Input.Wrapper.extend({
  styles: { label: { fontWeight: 700 } },
  vars: (_theme, props) => {
    const fz = FONT_SIZES[props.size]
    if (!fz) return { label: {}, error: {}, description: {} }
    return {
      label: { '--input-label-size': 'var(--mantine-font-size-sm)' },
      error: { '--input-error-size': `calc(${fz} - ${rem(2)})` },
      description: { '--input-description-size': `calc(${fz} - ${rem(2)})` },
    }
  },
})
