import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { Alert, Center, Stack, Text, Code } from '@mantine/core'

function loadCSS(href) {
  if (document.querySelector(`link[data-cljs-css]`)) return
  const link = document.createElement('link')
  link.rel = 'stylesheet'
  link.href = href
  link.setAttribute('data-cljs-css', 'true')
  document.head.appendChild(link)
}

/**
 * Loads the ClojureScript bundle.
 * Returns true if this is the first load (init() runs automatically via :init-fn).
 * Returns false if the bundle was already loaded (caller must trigger remount manually).
 */
function loadScript(src) {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[data-cljs-bundle]`)) {
      resolve(false) // already loaded — needs manual remount
      return
    }
    const script = document.createElement('script')
    script.src = src
    script.setAttribute('data-cljs-bundle', 'true')
    script.onload = () => resolve(true) // first load — init() fires automatically
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
  // Track whether the CLJS app is ready to receive route updates
  const cljsReadyRef = useRef(false)

  useEffect(() => {
    localStorage.setItem('react-shell', 'true')

    if (mountRef.current) {
      mountRef.current.id = 'app'
    }

    loadCSS('/css/site.css')

    loadScript('/js/app.js')
      .then((isFirstLoad) => {
        if (isFirstLoad) {
          // init() was already called by shadow-cljs :init-fn — nothing to do.
          // Pushy already parsed the current URL on startup.
          cljsReadyRef.current = true
          return
        }
        // Bundle was already loaded (user navigated away and came back).
        // Sync the active panel BEFORE remounting so Reagent renders the
        // correct page immediately (no wrong-panel flash).
        if (window.hoopSetRoute) {
          window.hoopSetRoute(window.location.pathname)
        }
        // Re-mount Reagent. Guards in app.cljs skip user/gateway refetches
        // when data already exists in the re-frame db.
        if (window.hoopRemount) {
          window.hoopRemount()
        }
        cljsReadyRef.current = true
      })
      .catch(() => {
        setError(true)
      })

    return () => {
      localStorage.removeItem('react-shell')
      cljsReadyRef.current = false
      if (mountRef.current) {
        mountRef.current.innerHTML = ''
        mountRef.current.id = ''
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

  return <div ref={mountRef} />
}

export default ClojureApp
