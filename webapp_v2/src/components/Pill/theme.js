import { Pill } from '@mantine/core'

// Global chip look (Figma: Neutral Alpha 3 background, Neutral 11 text,
// fully rounded). Pills render inside MultiSelect, TagsInput and every
// PillsInput composition, so this single extension keeps chips consistent
// across the app — matching the legacy webapp's react-select chips, which
// use the same values in webapp/src/webapp/components/multiselect.cljs.
// Instances that need a variant look (e.g. the managed protection-profile
// pill) override with `bg`/`c` style props.
export const PillTheme = Pill.extend({
  defaultProps: { radius: 'xl' },
  styles: {
    root: {
      backgroundColor: 'rgba(0, 0, 51, 0.06)',
      color: '#60646c',
    },
  },
})
