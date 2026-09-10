import { theme, cssVariablesResolver as baseResolver } from '@/theme'

// The control plane's theme: src/theme.js, with a softer disabled state.
//
// Mantine v8 paints every disabled control from three root tokens, and its
// light defaults resolve against this app's gray scale to a solid mid-gray
// (#c8c8cb) with no opacity behind it on Buttons (Button.css reads them
// directly). That reads far heavier than the rest of the UI. The Figma paints
// a disabled button with the Neutral Alpha 3 tint instead — the same tint
// Pill/theme.js and Sidebar.module.css already use for hover and active.
//
// The text token stays where it is on purpose. The Figma's disabled label is
// Neutral Alpha 8 (27%), which suits a button, but Mantine multiplies inputs
// by a further opacity: 0.6 (Input.css), and the wizard disables the Name
// field with the sidecar's name inside it. At 27% times 0.6 that value stops
// being readable.
//
// Only the control plane takes this. The gateway keeps Mantine's defaults
// through src/theme.js, so nothing there changes.
const DISABLED = {
  '--mantine-color-disabled': 'rgba(0, 0, 51, 0.06)',
  '--mantine-color-disabled-color': '#807f8f',
  '--mantine-color-disabled-border': '#e5e5e5',
}

// Delegates rather than copies: the base resolver owns --brand-navy, the
// control-height scale and the light bucket's body/text/dimmed/border/
// placeholder tokens. Two lists would drift.
//
// The light bucket is the only one that works. Mantine declares these under
// :root[data-mantine-color-scheme="light"], which outranks a plain :root
// declaration — the same reason the base resolver puts its own overrides
// there (see the comment in src/theme.js).
export function cssVariablesResolver(mantineTheme) {
  const base = baseResolver(mantineTheme)
  return {
    ...base,
    light: { ...base.light, ...DISABLED },
  }
}

export { theme }
