import { create } from 'zustand'

const INTERCOM_APP_ID = 'ryuapdmp'

function loadIntercomScript() {
  if (document.getElementById('intercom-script')) return
  const script = document.createElement('script')
  script.id = 'intercom-script'
  script.type = 'text/javascript'
  // Intercom loader snippet — creates a stub that queues calls until the real SDK loads
  script.innerHTML =
    "(function(){var w=window;var ic=w.Intercom;if(typeof ic===\"function\"){ic('reattach_activator');ic('update',w.intercomSettings);}else{var d=document;var i=function(){i.c(arguments);};i.q=[];i.c=function(args){i.q.push(args);};w.Intercom=i;var l=function(){var s=d.createElement('script');s.type='text/javascript';s.async=true;s.src='https://widget.intercom.io/widget/" +
    INTERCOM_APP_ID +
    "';var x=d.getElementsByTagName('script')[0];x.parentNode.insertBefore(s,x);};if(document.readyState==='complete'){l();}else if(w.attachEvent){w.attachEvent('onload',l);}else{w.addEventListener('load',l,false);}}})();"
  document.head.appendChild(script)
}

export const useUserStore = create((set, get) => ({
  user: null,
  isAdmin: false,
  isFreeLicense: true,
  analyticsTracking: false,
  gatewayVersion: null,
  loading: false,

  setUser: (user) => set({ user, isAdmin: !!user?.is_admin }),
  setServerInfo: (serverInfo) => {
    const license = serverInfo?.license_info
    const isFreeLicense = !(license?.is_valid && license?.type === 'enterprise')
    const analyticsTracking = serverInfo?.analytics_tracking === 'enabled'
    set({ isFreeLicense, gatewayVersion: serverInfo?.version || null, analyticsTracking })
  },
  setLoading: (loading) => set({ loading }),
  clear: () => {
    if (window.Intercom) window.Intercom('shutdown')
    set({ user: null, isAdmin: false, isFreeLicense: true, analyticsTracking: false, gatewayVersion: null })
  },

  initIntercom: (user) => {
    const { analyticsTracking } = get()
    if (!analyticsTracking) return

    if (window.Intercom) window.Intercom('shutdown')
    loadIntercomScript()

    const config = {
      api_base: 'https://api-iam.intercom.io',
      app_id: INTERCOM_APP_ID,
      hide_default_launcher: true,
    }

    if (window.location.hostname !== 'localhost' && user) {
      config.name = user.name
      config.email = user.email
      config.user_id = user.email
      config.user_hash = user.intercom_hmac_digest
    }

    // Script creates a stub immediately — safe to call boot right away
    window.Intercom('boot', config)
  },
}))
