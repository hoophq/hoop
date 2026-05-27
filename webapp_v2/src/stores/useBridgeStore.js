import { create } from 'zustand'
import { clojureDispatch } from '@/utils/clojureDispatch'

// Cross-cutting bridge calls into the legacy CLJS app. Per
// CLAUDE.md "Never call `window.hoopDispatch` directly from a
// component" — wrap every dispatch in a store method so the
// underlying mechanism can be swapped when the CLJS side is removed.
//
// snackbar: dispatched to CLJS so React pages share the same look
// (top-right, 10s, dark) as the legacy snackbar component. Levels
// match the CLJS handler at events/components/toast.cljs: 'success',
// 'error', 'info'.
export const useBridgeStore = create(() => ({
  showSnackbar: ({ level, text, details, description }) => {
    clojureDispatch('show-snackbar', { level, text, details, description })
  },
}))
