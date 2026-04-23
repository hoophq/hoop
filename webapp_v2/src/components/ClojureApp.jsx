/* global __BUILD_ID__ */
import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { Alert, Center, Stack, Text, Code } from '@mantine/core'
import PageLoader from '@/components/PageLoader'

// Cache buster defined by Vite at build time (see vite.config.js). A new value
// per build forces browsers (including in-memory cache) to refetch the CLJS
// assets instead of reusing a stale copy from a previous deploy.
const BUILD_ID = typeof __BUILD_ID__ !== 'undefined' ? __BUILD_ID__ : 'dev'

function withVersion(href) {
  return `${href}${href.includes('?') ? '&' : '?'}v=${BUILD_ID}`
}

function loadCSS(href) {
  if (document.querySelector(`link[data-cljs-css]`)) return
  const link = document.createElement('link')
  link.rel = 'stylesheet'
  link.href = withVersion(href)
  link.setAttribute('data-cljs-css', 'true')
  document.head.appendChild(link)
}

const INIT_POLL_INTERVAL_MS = 25
const INIT_POLL_TIMEOUT_MS = 10000

/**
 * Resolves once the CLJS bundle has finished running init() — detected by the
 * presence of window.hoopRemount (the last global set in webapp.core/init).
 * Polling is used because the <script>'s load event may have already fired.
 */
function waitForCljsInit() {
  if (window.hoopRemount) return Promise.resolve()
  return new Promise((resolve, reject) => {
    const started = Date.now()
    const id = setInterval(() => {
      if (window.hoopRemount) {
        clearInterval(id)
        resolve()
      } else if (Date.now() - started > INIT_POLL_TIMEOUT_MS) {
        clearInterval(id)
        reject(new Error('CLJS init() timeout'))
      }
    }, INIT_POLL_INTERVAL_MS)
  })
}

/**
 * Loads the ClojureScript bundle and resolves once webapp.core/init has
 * finished running (detected by `window.hoopRemount`). The caller is
 * always responsible for calling `hoopSetRoute` + `hoopRemount` to render
 * into the current mount div — init's own `mount-root` may have targeted
 * a stale or wiped `<div id="app">` (e.g. StrictMode cleanup between the
 * script's injection and its execution).
 */
function loadScript(src) {
  return new Promise((resolve, reject) => {
    const existing = document.querySelector(`script[data-cljs-bundle]`)
    if (existing) {
      waitForCljsInit().then(resolve).catch(reject)
      return
    }
    const script = document.createElement('script')
    script.src = withVersion(src)
    script.setAttribute('data-cljs-bundle', 'true')
    script.onload = () => waitForCljsInit().then(resolve).catch(reject)
    script.onerror = reject
    document.body.appendChild(script)
  })
}

/**
 * ClojureApp — mounts the ClojureScript/Reagent app inside the React shell.
 *
 * Requires the shadow-cljs dev server running on port 8280 (or VITE_CLJS_URL).
 * Start it with: cd webapp && npm run shadow:watch:hoop-ui
 */
function ClojureApp() {
  const location = useLocation()
  const mountRef = useRef(null)
  const [error, setError] = useState(null)
  // Show the loader on every ClojureApp mount. It is dismissed only after
  // we have explicitly called hoopRemount into *this* mount's div, which
  // guarantees the CLJS tree is actually rendered inside the visible DOM.
  const [cljsLoading, setCljsLoading] = useState(true)
  // Track whether the CLJS app is ready to receive route updates
  const cljsReadyRef = useRef(false)

  useEffect(() => {
    localStorage.setItem('react-shell', 'true')

    if (mountRef.current) {
      mountRef.current.id = 'app'
    }

    loadCSS('/css/site.css')

    loadScript('/js/app.js')
      .then(() => {
        // init() is guaranteed complete (window.hoopRemount is set).
        // init's own mount-root may have targeted a previous div that has
        // since been unmounted by React, or had its innerHTML wiped by a
        // StrictMode cleanup pass. Regardless of how we got here, sync
        // the active panel to the current URL and render Reagent into
        // *this* mount's div — which now holds id="app".
        if (mountRef.current && mountRef.current.id !== 'app') {
          mountRef.current.id = 'app'
        }
        window.hoopSetRoute(window.location.pathname)
        window.hoopRemount()
        cljsReadyRef.current = true
        setCljsLoading(false)
      })
      .catch(() => {
        setError(true)
        setCljsLoading(false)
      })

    return () => {
      localStorage.removeItem('react-shell')
      cljsReadyRef.current = false
      if (mountRef.current) {
        mountRef.current.innerHTML = ''
        // Do NOT clear id here — in React 18 StrictMode, cleanup runs before
        // re-mount while the DOM element stays. If the CLJS script loads during
        // that window it calls getElementById("app") and crashes on null.
      }
    }
  }, [])

  // Sync the CLJS active panel whenever React Router changes the pathname.
  // This handles sidebar navigation between ClojureScript routes without a
  // full remount — React Router pushes state but pushy only hears popstate,
  // so we bridge the gap by calling hoopSetRoute directly.
  useEffect(() => {
    if (cljsReadyRef.current && window.hoopSetRoute) {
      try {
        window.hoopSetRoute(location.pathname)
      } catch (e) {
        console.warn('[ClojureApp] hoopSetRoute failed for', location.pathname, e)
      }
    }
  }, [location.pathname])

  if (error) {
    return (
      <Center style={{ height: '60vh' }}>
        <Stack align="center" maw={480}>
          <Alert color="red" title="ClojureScript bundle not available" style={{ width: '100%' }}>
            <Text size="sm" mb="xs">
              The ClojureScript dev server is not running. Start it with:
            </Text>
            <Code block>
              cd webapp{'\\n'}
              npm run shadow:watch:hoop-ui{'\\n'}
              npm run postcss:watch
            </Code>
          </Alert>
        </Stack>
      </Center>
    )
  }

  return (
    <>
      {cljsLoading && <PageLoader overlay />}
      <div ref={mountRef} />
    </>
  )
}

export default ClojureApp
