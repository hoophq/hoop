import { useState, useEffect } from 'react'

// Keeps loading visible for at least `delay` ms after it goes false.
// Prevents flash when a request resolves faster than the minimum perceived duration.
export function useMinDelay(isLoading, delay = 500) {
  const [show, setShow] = useState(isLoading)

  useEffect(() => {
    // Always schedule via setTimeout to avoid synchronous setState in effect.
    // When isLoading is true, 0ms keeps it imperceptibly fast.
    const t = setTimeout(() => setShow(isLoading), isLoading ? 0 : delay)
    return () => clearTimeout(t)
  }, [isLoading, delay])

  return show
}
