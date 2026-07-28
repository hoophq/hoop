import { createTheme, rem } from '@mantine/core';
import { SpotlightTheme } from '@/components/Spotlight/theme';
import { AppShellTheme } from '@/components/AppShell/theme';
import { PillTheme } from '@/components/Pill/theme';

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

export function cssVariablesResolver() {
  return {
    variables: {
      // Brand navy — dark surface color for auth/upsell visuals (MethodCard,
      // SelectionCard, EnterpriseBanner, AuthPageLoader). Not tied to the
      // sidebar, which uses the gray scale.
      '--brand-navy': '#182449',
      // Radix Slate steps 11/12 are not in the 10-slot gray array — wired here
      // as semantic tokens (see the gray scale comment below).
      '--mantine-color-body': '#fcfcfd', // near-white — page background (gray[0] #f0f0f3 is too tinted for body)
      '--mantine-color-text': '#212529', // slate12 — body text, headings
      '--mantine-color-dimmed': '#60646c', // slate11 — secondary text, icons
      '--mantine-color-placeholder': '#807f8f' // gray[5] — placeholder text
    },
    light: {},
    dark: {}
  };
}

export const theme = createTheme({
  primaryColor: 'indigo',
  primaryShade: 8, // → Radix shade 9, the solid/saturated action color
  defaultRadius: 'md',

  fontFamily: "Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', system-ui, sans-serif",
  fontFamilyMonospace: "Menlo, Consolas, 'Bitstream Vera Sans Mono', monospace",

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
      '#fbfefc', // shade 1
      '#f4fbf6', // shade 2
      '#e6f6eb', // shade 3
      '#d6f1df', // shade 4
      '#c4e8d1', // shade 5
      '#adddc0', // shade 6
      '#8eceaa', // shade 7
      '#5bb98b', // shade 8
      '#30a46c', // shade 9  ← primaryShade (index 8)
      '#2b9a66' // shade 10
    ],

    // Radix Amber — warning / caution
    amber: [
      '#fefdfb', // shade 1
      '#fefbe9', // shade 2
      '#fff7c2', // shade 3
      '#ffee9c', // shade 4
      '#fbe577', // shade 5
      '#f3d673', // shade 6
      '#e9c162', // shade 7
      '#e2a336', // shade 8
      '#ffc53d', // shade 9  ← primaryShade (index 8)
      '#ffba18' // shade 10
    ],

    // Radix Red — error / destructive actions
    red: [
      '#fffcfc', // shade 1
      '#fff7f7', // shade 2
      '#feebec', // shade 3
      '#ffdbdc', // shade 4
      '#ffcdce', // shade 5
      '#fdbdbe', // shade 6
      '#f4a9aa', // shade 7
      '#eb8e90', // shade 8
      '#e5484d', // shade 9  ← primaryShade (index 8)
      '#dc3e42' // shade 10
    ],

    // Radix Sky — informational / neutral highlight
    sky: [
      '#f9feff', // shade 1
      '#f1fafd', // shade 2
      '#e1f6fd', // shade 3
      '#d1f0fa', // shade 4
      '#bee7f5', // shade 5
      '#a9daed', // shade 6
      '#8dcae3', // shade 7
      '#60b3d7', // shade 8
      '#7ce2fe', // shade 9  ← primaryShade (index 8)
      '#74daf8' // shade 10
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
    Pill: PillTheme
  }
});
