import { createTheme, DEFAULT_THEME, rem } from '@mantine/core';
import { ActionIconTheme } from '@/components/ActionIcon/theme';
import { AppShellTheme } from '@/components/AppShell/theme';
import { ButtonTheme } from '@/components/Button/theme';
import {
  InputTheme,
  InputBaseTheme,
  InputWrapperTheme,
  TextareaTheme,
  MultiSelectTheme,
  TagsInputTheme,
  PillsInputTheme,
} from '@/components/Input/theme';
import { PaperTheme } from '@/components/Paper/theme';
import { PillTheme } from '@/components/Pill/theme';
import { SpotlightTheme } from '@/components/Spotlight/theme';

// The control plane theme.
//
// Derived from webapp_v2 with everything that existed to match the ClojureScript
// app's Radix look removed — there is no ClojureScript here, so nothing to match.
// What stayed is hoop's own design language: the colour ramps, the radius scale, the
// heading sizes, and the 40px control scale.

export function cssVariablesResolver(theme) {
  return {
    variables: {
      // Dark surface for auth and upsell visuals (MethodCard, SelectionCard,
      // EnterpriseBanner, AuthPageLoader). Brand identity, so it is a fixed value
      // rather than a scheme-dependent token.
      '--brand-navy': '#1F2D5C',

      // Pure white. theme.white is #FCFCFD — the same as --mantine-color-body, so it
      // cannot separate a raised surface from the page.
      '--hoop-surface-raised': '#ffffff',

      // Single source of truth for the height of every control. Consumed by the
      // Button/ActionIcon/Input vars resolvers below and read directly by
      // SourcedInput.module.css.
      '--hoop-control-height-xs': rem(24),
      '--hoop-control-height-sm': rem(32),
      '--hoop-control-height-md': rem(40),
      '--hoop-control-height-lg': rem(48)
    },
    // Scheme-dependent semantic tokens. Mantine emits its own defaults under
    // :root[data-mantine-color-scheme="light"], which outranks a plain :root
    // declaration, so overrides have to live in this bucket to win deterministically.
    //
    // Component-scoped variables (--input-bd, --paper-border-color, …) cannot be
    // overridden here at all: Mantine declares them on the component element, which
    // beats inheritance. Those go through Component.extend() or the component's own
    // CSS Module.
    light: {
      '--mantine-color-body': '#fcfcfd', // page background; gray[0] is too tinted for it
      '--mantine-color-text': '#212529', // slate12 — body text and headings
      '--mantine-color-dimmed': '#60646c', // slate11 — ~5.7:1 on white, passes WCAG AA
      '--mantine-color-placeholder': '#807f8f',
      // Mantine would derive this from gray[4] (#90909c on our ramp), far heavier than
      // the hairline the design uses. Setting it here rather than hardcoding gray-2 in
      // each CSS Module is what lets borders follow the colour scheme at all: palette
      // steps are constant across light and dark, semantic tokens are not.
      '--mantine-color-default-border': theme.colors.gray[2]
    },
    // Left to Mantine. Dark mode is not a shipped feature; when it becomes one, this is
    // where the overrides go, and the components that already read semantic tokens
    // follow for free. See CLAUDE.md "Colour scheme" for what still would not.
    dark: {}
  };
}

export const theme = createTheme({
  primaryColor: 'indigo',
  primaryShade: 5,
  defaultRadius: 'md',
  white: '#FCFCFD',
  black: '#212529',

  spacing: {
    xsAlt: rem(4),
    smAlt: rem(8),
    mdAlt: rem(16),
    lgAlt: rem(24),
    xlAlt: rem(32),
    xxlAlt: rem(48),
    xxxlAlt: rem(64)
  },

  radius: {
    xs: '4.5px',
    sm: '6px',
    md: '9px',
    lg: '12px',
    xl: '18px'
  },

  fontSizes: {
    xs: rem(12),
    sm: rem(14),
    md: rem(16),
    lg: rem(18),
    xl: rem(20)
  },

  lineHeights: {
    xs: '1.4',
    sm: '1.45',
    md: '1.5',
    lg: '1.55',
    xl: '1.6'
  },

  headings: {
    sizes: {
      h1: { fontSize: rem(36), fontWeight: '700', lineHeight: '1.3' },
      h2: { fontSize: rem(24), fontWeight: '700', lineHeight: '1.35' },
      h3: { fontSize: rem(20), fontWeight: '600', lineHeight: '1.4' },
      h4: { fontSize: rem(18), fontWeight: '600', lineHeight: '1.45' },
      h5: { fontSize: rem(16), fontWeight: '500', lineHeight: '1.5' },
      h6: { fontSize: rem(14), fontWeight: '500', lineHeight: '1.5' }
    }
  },

  colors: {
    // Mantine ships no `amber` or `sky`, and both are referenced by name on Alert and
    // Badge. Unresolved, those call sites lose their colour silently. Aliased to the
    // closest stock ramps.
    amber: DEFAULT_THEME.colors.yellow,
    sky: DEFAULT_THEME.colors.cyan,

    gray: [
      '#f0f0f3',
      '#e5e5e5',
      '#c8c8cb',
      '#aaaab2',
      '#90909c',
      '#807f8f',
      '#77778a',
      '#666577',
      '#5a5a6c',
      '#4d4d60'
    ],
    indigo: [
      '#eaf1ff',
      '#d5defd',
      '#a9bbf3',
      '#7b95e9',
      '#5475e1',
      '#3e63dd',
      '#2c56db',
      '#1d47c3',
      '#153fb0',
      '#02359c'
    ]
  },

  // Kept from webapp_v2 because each one is a hoop decision, not Radix compatibility:
  // Button/ActionIcon/Input hold the 40px control scale, Pill pins chips to 22px so
  // they stop inheriting the field size and overflowing a MultiSelect, AppShell owns
  // the sidebar collapse transition, Paper and Spotlight own borders and palette rows.
  components: {
    ActionIcon: ActionIconTheme,
    AppShell: AppShellTheme,
    Button: ButtonTheme,
    Input: InputTheme,
    InputBase: InputBaseTheme,
    InputWrapper: InputWrapperTheme,
    Textarea: TextareaTheme,
    MultiSelect: MultiSelectTheme,
    TagsInput: TagsInputTheme,
    PillsInput: PillsInputTheme,
    PickerInputBase: { defaultProps: { size: 'md' } },
    Paper: PaperTheme,
    Pill: PillTheme,
    Spotlight: SpotlightTheme
  }
});
