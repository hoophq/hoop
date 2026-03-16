import { useEffect, useRef, useState } from 'react'
import { Alert, Center, Loader, Stack, Text, Code } from '@mantine/core'

function loadCSS(href) {
  if (document.querySelector(`link[data-cljs-css]`)) return
  const link = document.createElement('link')
  link.rel = 'stylesheet'
  link.href = href
  link.setAttribute('data-cljs-css', 'true')
  document.head.appendChild(link)
}

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
 * Requires the shadow-cljs dev server running on port 8280 (or VITE_CLJS_URL).
 * Start it with: cd webapp && npm run shadow:watch:hoop-ui
 */
function ClojureApp() {
  const mountRef = useRef(null)
  const [error, setError] = useState(null)

  useEffect(() => {
    localStorage.setItem('react-shell', 'true')

    if (mountRef.current) {
      mountRef.current.id = 'app'
    }

    loadCSS('/css/site.css')

    loadScript('/js/app.js')
      .then(() => {
        if (window.hoopRemount) {
          window.hoopRemount()
        }
      })
      .catch(() => {
        setError(true)
      })

    return () => {
      localStorage.removeItem('react-shell')
      if (mountRef.current) {
        mountRef.current.innerHTML = ''
        mountRef.current.id = ''
      }
    }
  }, [])

  if (error) {
    return (
      <Center style={{ height: '60vh' }}>
        <Stack align="center" maw={480}>
          <Alert color="red" title="ClojureScript bundle not available" style={{ width: '100%' }}>
            <Text size="sm" mb="xs">
              The ClojureScript dev server is not running. Start it with:
            </Text>
            <Code block>
              cd webapp{'\n'}
              npm run shadow:watch:hoop-ui{'\n'}
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
