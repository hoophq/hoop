/* global __BUILD_ID__ */
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
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

// The CLJS app ships its own Tailwind/Radix stylesheet. It would otherwise
// leak into every React page (the <link> lives in <head> forever), so we
// toggle `link.disabled` on ClojureApp mount/unmount: the browser keeps the
// parsed stylesheet in memory, remounts are instant and flash-free, and its
// rules only apply while a CLJS route is actually rendered.
function enableCLJSCSS(href) {
  const existing = document.querySelector('link[data-cljs-css]')
  if (existing) {
    existing.disabled = false
    return
  }
  const link = document.createElement('link')
  link.rel = 'stylesheet'
  link.href = withVersion(href)
  link.setAttribute('data-cljs-css', 'true')
  document.head.appendChild(link)
}

function disableCLJSCSS() {
  const link = document.querySelector('link[data-cljs-css]')
  if (link) link.disabled = true
}

// Module-level singletons — these survive every ClojureApp mount/unmount cycle
// so the Reagent tree rendered inside `cljsHost` is never destroyed once built.
// When ClojureApp unmounts we move the host into a hidden parking node; on the
// next mount we move it back into the visible container. DOM subtree (and thus
// React/Reagent fiber state) stays alive across the whole app lifetime.
let cljsHost = null
let cljsParking = null
let cljsInitialized = false
// Last pathname synced into the CLJS active-panel. When ClojureApp re-mounts
// on the same path (e.g. /dashboard → /agents → /dashboard), we can skip the
// re-sync and reveal the host immediately — the tree already shows the right
// panel. When the path is stale, we hide the host for a frame while re-frame
// propagates the new active-panel to Reagent, so the user never sees the old
// panel flash before the new one renders.
let lastSyncedPath = null

function getParkingNode() {
  if (!cljsParking) {
    cljsParking = document.createElement('div')
    cljsParking.setAttribute('data-cljs-parking', 'true')
    cljsParking.style.display = 'none'
    document.body.appendChild(cljsParking)
  }
  return cljsParking
}

function getCljsHost() {
  if (!cljsHost) {
    cljsHost = document.createElement('div')
    cljsHost.id = 'app'
    // Park it until a ClojureApp instance claims it. mount-root in the CLJS
    // bundle looks up `#app` via getElementById — parking it inside <body>
    // keeps that lookup working even if the bundle's init() fires between
    // our cleanup and next mount (e.g. in React 18 StrictMode double-effect).
    getParkingNode().appendChild(cljsHost)
  }
  return cljsHost
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
 * ClojureApp — bridges the ClojureScript/Reagent app into the React shell.
 *
 * The CLJS tree lives inside a singleton `<div id="app">` that is created
 * once per page load and reused across every mount. On unmount the host is
 * moved into a hidden parking node (not destroyed), so switching between a
 * React route and a CLJS route does NOT trigger a Reagent re-render — the
 * old flash of blank content + loader is gone.
 *
 * Route changes are propagated via `window.hoopSetRoute`; re-frame then
 * updates the active panel and Reagent reconciles into the existing DOM.
 *
 * Requires the shadow-cljs dev server running on port 8280 (or VITE_CLJS_URL).
 * Start it with: cd webapp && npm run shadow:watch:hoop-ui
 */
function ClojureApp() {
  const location = useLocation()
  const containerRef = useRef(null)
  const [error, setError] = useState(null)
  // Only show the loader on the very first mount — when cljsInitialized is
  // already true the host already holds a rendered Reagent tree, and the
  // handoff is instant.
  const [cljsLoading, setCljsLoading] = useState(!cljsInitialized)
  const cljsReadyRef = useRef(cljsInitialized)

  // useLayoutEffect so the parking↔container swap happens before the browser
  // paints the empty <div ref={containerRef} /> React just rendered.
  useLayoutEffect(() => {
    localStorage.setItem('react-shell', 'true')
    enableCLJSCSS('/css/site.css')

    const host = getCljsHost()
    const currentPath = window.location.pathname
    const needsResync = cljsInitialized && lastSyncedPath !== currentPath

    if (needsResync) {
      // Mask the host while re-frame swaps the active panel. Without this
      // the user sees one frame of the previous panel (whatever was rendered
      // before unmount) before Reagent reconciles to the new one.
      host.style.visibility = 'hidden'
    }

    if (containerRef.current && host.parentNode !== containerRef.current) {
      // appendChild auto-detaches from the parking node; the subtree (and
      // any React/Reagent fiber state) comes with it.
      containerRef.current.appendChild(host)
    }

    if (cljsInitialized) {
      try {
        window.hoopSetRoute(currentPath)
        lastSyncedPath = currentPath
      } catch (e) {
        console.warn('[ClojureApp] hoopSetRoute failed on remount', e)
      }
      cljsReadyRef.current = true
      if (needsResync) {
        // Two RAFs: first commits the re-frame-triggered Reagent render,
        // second paints it. Reveal after that so the first visible frame
        // already shows the new panel.
        requestAnimationFrame(() => {
          requestAnimationFrame(() => {
            host.style.visibility = ''
          })
        })
      }
    } else {
      loadScript('/js/app.js')
        .then(() => {
          // init() has already called mount-root into #app by the time
          // waitForCljsInit resolves. The host is parked in <body> while
          // detached, so getElementById("app") still finds it — Reagent's
          // initial render lands in the live singleton either way. We only
          // need to align the active panel with the current URL.
          window.hoopSetRoute(currentPath)
          lastSyncedPath = currentPath
          cljsInitialized = true
          cljsReadyRef.current = true
          setCljsLoading(false)
        })
        .catch(() => {
          setError(true)
          setCljsLoading(false)
        })
    }

    return () => {
      localStorage.removeItem('react-shell')
      cljsReadyRef.current = false
      disableCLJSCSS()
      // Park the host. Do NOT clear innerHTML — preserving the Reagent tree
      // across mounts is the whole point of the singleton pattern.
      if (host.parentNode) {
        getParkingNode().appendChild(host)
      }
    }
  }, [])

  // Sync the CLJS active panel whenever React Router changes the pathname
  // while ClojureApp is mounted (e.g. sidebar navigation between CLJS routes).
  useEffect(() => {
    if (cljsReadyRef.current && window.hoopSetRoute) {
      try {
        window.hoopSetRoute(location.pathname)
        lastSyncedPath = location.pathname
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
      {cljsLoading && <PageLoader />}
      <div ref={containerRef} />
    </>
  )
}

export default ClojureApp
