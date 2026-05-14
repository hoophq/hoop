// Bridge to dispatch Re-frame events from React.
// Usage: clojureDispatch('event-name', arg1, arg2, ...)
//
// Remove this file — and the hoopDispatch assignment in webapp/src/webapp/core.cljs —
// once the ClojureScript bundle is fully removed.

export function clojureDispatch(event, ...args) {
  if (typeof window.hoopDispatch !== 'function') {
    console.warn('[clojureDispatch] CLJS not ready, event dropped:', event, args)
    return
  }
  window.hoopDispatch([event, ...args])
}
