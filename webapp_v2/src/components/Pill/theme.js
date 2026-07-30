import { Pill, rem } from '@mantine/core'

// Global chip look (Figma: Neutral Alpha 3 background, Neutral 11 text,
// fully rounded). Pills render inside MultiSelect, TagsInput and every
// PillsInput composition, so this single extension keeps chips consistent
// across the app — matching the legacy webapp's react-select chips, which
// use the same values in webapp/src/webapp/components/multiselect.cljs.
// Instances that need a variant look (e.g. the managed protection-profile
// pill) override with `bg`/`c` style props.
export const PillTheme = Pill.extend({
  defaultProps: { radius: 'xl' },
  // One chip size everywhere, like the legacy react-select chips: 22px tall,
  // 12px text, regardless of the host input's size. Pills inside a
  // MultiSelect/TagsInput otherwise inherit the field's size, which can
  // overflow the 1.6em inner row that the Input theme's multiline padding
  // is calibrated against, pushing the field past its scale height. 22px
  // sits inside that row for sm and up (22.4px at sm, 25.6px at md).
  vars: () => ({
    root: {
      '--pill-height': rem(22),
      '--pill-fz': 'var(--mantine-font-size-xs)',
    },
  }),
  styles: {
    root: {
      backgroundColor: 'rgba(0, 0, 51, 0.06)',
      color: '#60646c',
    },
  },
})
