import { useEffect, useRef } from 'react'
import { Center, Loader } from '@mantine/core'

/**
 * Loads a CSS file once. Marks it with data-cljs to avoid duplicates.
 */
function loadCSS(href) {
  if (document.querySelector(`link[data-cljs-css]`)) return
  const link = document.createElement('link')
  link.rel = 'stylesheet'
  link.href = href
  link.setAttribute('data-cljs-css', 'true')
  document.head.appendChild(link)
}

/**
 * Loads the ClojureScript bundle once.
 * On first call: appends <script> and resolves after load (auto-calls webapp.core/init).
 * On subsequent calls: resolves immediately (bundle already loaded).
 */
function loadScript(src) {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[data-cljs-bundle]`)) {
      resolve()
      return
    }
    const script = document.createElement('script')
    script.src = src
    script.setAttribute('data-cljs-bundle', 'true')
    script.onload = resolve
    script.onerror = reject
    document.body.appendChild(script)
  })
}

/**
 * ClojureApp — mounts the ClojureScript/Reagent app inside the React shell.
 *
 * Strategy:
 * - Sets `react-shell` in localStorage so the Clojure app skips its own sidebar/cmdk
 * - First mount: loads the bundle (auto-calls webapp.core/init)
 * - Subsequent mounts (user navigated to a React page and came back): calls
 *   window.hoopRemount() which was set by core.cljs during init
 * - Unmount: cleans up the DOM and removes the react-shell flag
 */
function ClojureApp() {
  const mountRef = useRef(null)

  useEffect(() => {
    localStorage.setItem('react-shell', 'true')

    // Ensure the Reagent mount point has id="app"
    if (mountRef.current) {
      mountRef.current.id = 'app'
    }

    loadCSS('/css/site.css')

    loadScript('/js/app.js')
      .then(() => {
        // Bundle already loaded and init already ran — just remount Reagent
        if (window.hoopRemount) {
          window.hoopRemount()
        }
      })
      .catch((err) => {
        console.error('[ClojureApp] Failed to load ClojureScript bundle:', err)
      })

    return () => {
      localStorage.removeItem('react-shell')
      if (mountRef.current) {
        mountRef.current.innerHTML = ''
        mountRef.current.id = ''
      }
    }
  }, [])

  return <div ref={mountRef} />
}

export default ClojureApp
