import { createTheme, rem } from '@mantine/core';
import { SpotlightTheme } from '@/components/Spotlight/theme';
import { AppShellTheme } from '@/components/AppShell/theme';
import { PillTheme } from '@/components/Pill/theme';
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
import { ButtonTheme } from '@/components/Button/theme';
import { ActionIconTheme } from '@/components/ActionIcon/theme';

// Design tokens mapped from the legacy webapp's Radix UI + Tailwind configuration.
//
// Color mapping: Radix uses a 12-step scale; Mantine uses 10 steps (indices 0–9).
// We map Radix shades 1–10, so index[8] = Radix shade 9 (the solid/saturated main color).
// primaryShade: 8 applies to all semantic palettes.
//
// Radius: Radix Theme uses radius="large" (factor 1.5).
// The resulting values match tailwind.config.js: { 1: 4.5px, 2: 6px, 3: 9px, 4: 12px, 5: 18px }
//
// Spacing: Radix --space-* scale (4px base increment).
//
// Font sizes: Radix --font-size-* scale (12/14/16/18/20px for xs–xl).

export function cssVariablesResolver(theme) {
  return {
    variables: {
      // Brand navy — dark surface color for auth/upsell visuals (MethodCard,
      // SelectionCard, EnterpriseBanner, AuthPageLoader) and the CLJS
      // enterprise banner. Not tied to the sidebar, which uses the gray scale.
      '--brand-navy': '#1F2D5C',

      // Control height scale — the single source of truth for Button,
      // ActionIcon, and every Input-based component. md is the app-wide
      // default (no size prop at call sites); xs/sm/lg are the variants.
      // Consumed by the vars resolvers in components/{Button,ActionIcon,Input}/theme.js
      // and by CSS Modules that must match a control's height (SourcedInput).
      '--hoop-control-height-xs': rem(24),
      '--hoop-control-height-sm': rem(32),
      '--hoop-control-height-md': rem(40),
      '--hoop-control-height-lg': rem(48),
    },
    // Scheme-dependent semantic tokens. Mantine emits its own defaults for
    // these under :root[data-mantine-color-scheme="light"], which outranks a
    // plain :root declaration — so overrides MUST live in this bucket, not in
    // `variables`, to win deterministically.
    //
    // Note: component-scoped variables (--input-bd, --paper-border-color,
    // --tab-hover-color, ...) cannot be overridden here at all — Mantine
    // declares them directly on the component element, which beats any
    // inherited value. Override those via Component.extend() in
    // src/components/[Name]/theme.js (see components/Input/theme.js).
    light: {
      // Radix Slate steps 11/12 are not in the 10-slot gray array — wired here
      // as semantic tokens (see the gray scale comment below).
      '--mantine-color-body': '#fcfcfd', // near-white — page background (gray[0] #f0f0f3 is too tinted for body)
      '--mantine-color-text': '#212529', // slate12 — body text, headings
      '--mantine-color-dimmed': '#60646c', // slate11 — secondary text, icons
      '--mantine-color-default-border': theme.colors.gray[2],
      '--mantine-color-placeholder': '#807f8f', // gray[5] — placeholder text
    },
    dark: {}
  };
}

export const theme = createTheme({
  primaryColor: 'indigo',
  primaryShade: 5, // → Radix shade 9, the solid/saturated action color
  defaultRadius: 'md',

  fontFamily: "Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', system-ui, sans-serif",
  fontFamilyMonospace: "Menlo, Consolas, 'Bitstream Vera Sans Mono', monospace",
  white: "#FCFCFD",
  black: "#212529",

  colors: {
    // Radix Indigo — primary action color
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
    ],

    // Slate-tinted neutral ramp (light mode), anchored on the Figma hoop/colors
    // gray tokens (gray/0 #f0f0f3 … gray/9 #4d4d60). The Radix Slate text steps
    // are not in this array — they are wired as semantic tokens in
    // cssVariablesResolver:
    //   --mantine-color-dimmed → slate11 #60646c (secondary text, icons)
    //   --mantine-color-text   → slate12 #1c2024 (body text, headings)
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

    // Radix Green — success / positive feedback
    green: [
      "#e9fbf0",
      "#d1f1dd",
      "#b9e6cb",
      "#8bcea5",
      "#5cb67f",
      "#1f9d57",   // shade 5 — primary in light
      "#1a8c4e",
      "#157c45",
      "#116c3c",
      "#0d5c33"
    ],

    // Radix Amber — warning / caution
    amber: [
      "#fff8e1",
      "#fcefc5",
      "#f8e6a8",
      "#efd180",
      "#e7bb53",
      "#e0a400",
      "#c59100",
      "#ab7e00",
      "#926c00",
      "#7a5a00"
    ],

    // Radix Red — error / destructive actions
    red: [
      "#ffe9ea",
      "#fdbdbe",
      "#faa0a1",
      "#f86b6d",
      "#f64141",
      "#f52825",
      "#f61a17",
      "#db0f0d",
      "#c40609",
      "#ab0004"
    ],

    // Radix Sky — informational / neutral highlight
    sky: [
      "#e5faff",
      "#d5eff9",
      "#a9daed",
      "#82c8e3",
      "#5fb7da",
      "#48acd5",
      "#39a7d3",
      "#2792bc",
      "#1582a9",
      "#007196"
    ]
  },
  spacing: {
    xsAlt: rem(4),
    smAlt: rem(8),
    mdAlt: rem(16),
    lgAlt: rem(24),
    xlAlt: rem(32),
    xxlAlt: rem(48),
    xxxlAlt: rem(64)
  },

  // Radix radius="large" (radius-factor: 1.5 × base values).
  // Exact values from tailwind.config.js borderRadius: { 1–5 }
  radius: {
    xs: '4.5px',
    sm: '6px',
    md: '9px',
    lg: '12px',
    xl: '18px'
  },

  // Radix --font-size-* scale
  fontSizes: {
    xs: rem(12), // --font-size-1
    sm: rem(14), // --font-size-2
    md: rem(16), // --font-size-3 (body default)
    lg: rem(18), // --font-size-4
    xl: rem(20) // --font-size-5
  },

  lineHeights: {
    xs: '1.4',
    sm: '1.45',
    md: '1.5',
    lg: '1.55',
    xl: '1.6'
  },

  // h1=--font-size-8 (36px) … h6=--font-size-2 (14px) — mirrors Radix size="8" used in CLJS page titles
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

  components: {
    Spotlight: SpotlightTheme,
    AppShell: AppShellTheme,
    Pill: PillTheme,
    Input: InputTheme,
    // Input-family components with local `size: 'sm'` defaults that would
    // otherwise beat the Input theme default — see components/Input/theme.js.
    InputBase: InputBaseTheme,
    InputWrapper: InputWrapperTheme,
    Textarea: TextareaTheme,
    MultiSelect: MultiSelectTheme,
    TagsInput: TagsInputTheme,
    PillsInput: PillsInputTheme,
    // PickerInputBase (under @mantine/dates DatePickerInput) is not exported,
    // so a plain theme entry stands in for Component.extend().
    PickerInputBase: { defaultProps: { size: 'md' } },
    Paper: PaperTheme,
    Button: ButtonTheme,
    ActionIcon: ActionIconTheme,
  }
});

