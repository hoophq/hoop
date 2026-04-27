import { createTheme, rem } from '@mantine/core';
import { SpotlightTheme } from '@/components/Spotlight/theme';
import { AppShellTheme } from '@/components/AppShell/theme';

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

export const theme = createTheme({
  primaryColor: 'indigo',
  primaryShade: 8, // → Radix shade 9, the solid/saturated action color
  defaultRadius: 'md',

  fontFamily: "Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', system-ui, sans-serif",
  fontFamilyMonospace: "Menlo, Consolas, 'Bitstream Vera Sans Mono', monospace",

  colors: {
    // Radix Indigo — primary action color
    indigo: [
      '#fdfdfe', // shade 1  — near-white tint
      '#f7f9ff', // shade 2
      '#edf2fe', // shade 3
      '#e1e9ff', // shade 4
      '#d2deff', // shade 5
      '#c1d0ff', // shade 6
      '#abbdf9', // shade 7
      '#8da4ef', // shade 8
      '#3e63dd', // shade 9  ← primaryShade (index 8)
      '#3358d4' // shade 10
    ],

    // Radix Slate — neutral scale (light mode)
    // Indices 0–7 = slate1–8 (backgrounds, borders, subtle fills)
    // Indices 8–9 = slate11–12 (text: low-contrast / high-contrast)
    // slate9/10 skipped — they sit between border and text ranges, rarely used directly
    gray: [
      '#fcfcfd', // slate1  — app background
      '#f9f9fb', // slate2  — subtle background
      '#f0f0f3', // slate3  — hovered background
      '#e8e8ec', // slate4  — selected/active background
      '#e0e1e6', // slate5  — subtle border
      '#d9d9e0', // slate6  — border
      '#cdced6', // slate7  — hovered border
      '#b9bbc6', // slate8  — solid, contrast fills
      '#60646c', // slate11 — low-contrast text (dimmed, placeholders, icons)
      '#1c2024' // slate12 — high-contrast text (body, headings)
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

  // 4pt base scale with exponential steps.
  // xs=4  sm=8  md=16  lg=24  xl=32  xxl=48  xxxl=64
  spacing: {
    xs: rem(4),
    sm: rem(8),
    md: rem(16),
    lg: rem(24),
    xl: rem(32),
    xxl: rem(48),
    xxxl: rem(64)
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
    AppShell: AppShellTheme
  }
});
