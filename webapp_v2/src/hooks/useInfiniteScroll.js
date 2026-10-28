import { useEffect, useRef } from 'react'

// Returns a ref for a sentinel element at the end of a list; `onLoadMore` runs
// when it scrolls into view. Re-observing when `enabled` turns true again fires
// at once if the sentinel is still visible, so a short page keeps loading.
export function useInfiniteScroll(onLoadMore, enabled = true) {
  const ref = useRef(null)
  const callback = useRef(onLoadMore)

  useEffect(() => {
    callback.current = onLoadMore
  }, [onLoadMore])

  useEffect(() => {
    const el = ref.current
    if (!el || !enabled) return undefined
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting) callback.current()
      },
      { rootMargin: '200px' },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [enabled])

  return ref
}
