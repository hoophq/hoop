import { Modal } from '@mantine/core'

// Every modal dims the page at 40%, as the CLJS (Radix) dialogs do; Mantine's
// default is 60%. An instance that passes overlayProps replaces this.
export const ModalTheme = Modal.extend({
  defaultProps: { overlayProps: { backgroundOpacity: 0.4 } },
})
