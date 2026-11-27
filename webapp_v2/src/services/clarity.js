const CLARITY_ID = 'h9osgp95be'
// The CLJS bundle checks this id too (events/tracking.cljs), so only one tag loads.
const SCRIPT_ID = 'clarity-script'
const STOPPED_HOSTS = ['localhost', '127.0.0.1', 'appdemo.hoop.dev', 'tryrunops.hoop.dev']

// Same rules as CLJS :clarity->verify-environment (events/clarity.cljs).
export function clarityCommand({ analyticsTracking, hostname, orgName }) {
  if (!analyticsTracking) return 'stop'
  if (STOPPED_HOSTS.includes(hostname) || orgName === 'hoopdev') return 'stop'
  return 'start'
}

function load() {
  if (document.getElementById(SCRIPT_ID)) return
  window.clarity =
    window.clarity ||
    function () {
      // Clarity replays this queue of `arguments` objects once its tag loads.
      ;(window.clarity.q = window.clarity.q || []).push(arguments)
    }
  const script = document.createElement('script')
  script.id = SCRIPT_ID
  script.async = true
  script.src = `https://www.clarity.ms/tag/${CLARITY_ID}`
  document.head.appendChild(script)
}

export function initClarity({ analyticsTracking, user }) {
  if (analyticsTracking) load()
  if (typeof window.clarity !== 'function') return
  window.clarity(clarityCommand({ analyticsTracking, hostname: window.location.hostname, orgName: user?.org_name }))
}
