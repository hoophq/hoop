import { create } from 'zustand'
import { identify as analyticsIdentify } from '@/services/analytics'
import { ROLE_ADMIN, ROLE_APPROVER, ROLE_STANDARD } from '@/utils/roles'

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
  role: ROLE_STANDARD,
  isAdmin: false,
  isApprover: false,
  isSelfHosted: false,
  isFreeLicense: true,
  analyticsTracking: false,
  analyticsMode: 'anonymous',
  disableClipboard: false,
  gatewayVersion: null,
  redactProvider: null,
  featureFlags: {},
  // Features enabled by the gateway license; empty/null = all enabled.
  // Only meaningful once serverInfoLoaded is true — gating fails closed
  // while /serverinfo hasn't been fetched successfully.
  licenseFeatures: null,
  // Null until /serverinfo resolves, so consumers must not guess a state.
  licenseInfo: null,
  serverInfoLoaded: false,
  apiUrl: null,
  hasRedactCredentials: false,
  // /serverinfo postgres_proxy_enabled. Fail closed: without a Postgres proxy
  // listen address the gateway cannot serve a native postgres session.
  postgresProxyEnabled: false,
  // Which product the backend runs as: 'gateway' or 'control-plane'. Null
  // until /serverinfo resolves. Both serve the same routes; the control plane
  // starts no gRPC transport, proxies or plugins. layout/ModeBanner reads it.
  applicationMode: null,
  adminRoleName: ROLE_ADMIN,
  approverRoleName: ROLE_APPROVER,
  loading: false,

  setUser: (user) => {
    const role = user?.role || ROLE_STANDARD
    set({
      user,
      role,
      isAdmin: role === ROLE_ADMIN,
      isApprover: role === ROLE_APPROVER,
      isSelfHosted: user?.tenancy_type === 'selfhosted'
    })
  },
  setServerInfo: (serverInfo) => {
    const license = serverInfo?.license_info
    const isFreeLicense = !(license?.is_valid && license?.type === 'enterprise')
    const analyticsTracking = serverInfo?.analytics_tracking === 'enabled'
    const analyticsMode = serverInfo?.analytics_mode || 'anonymous'
    const disableClipboard = !!serverInfo?.disable_clipboard_copy_cut
    const featureFlags = serverInfo?.feature_flags || {}
    const redactProvider = serverInfo?.redact_provider || null
    const apiUrl = serverInfo?.api_url || null
    const licenseFeatures = serverInfo?.license_info?.features || null
    set({ 
      isFreeLicense, 
      gatewayVersion: serverInfo?.version || null, 
      analyticsTracking, 
      analyticsMode, 
      disableClipboard, 
      featureFlags, 
      redactProvider, 
      apiUrl,
      licenseFeatures,
      licenseInfo: license || null,
      serverInfoLoaded: true,
      hasRedactCredentials: !!serverInfo?.has_redact_credentials,
      postgresProxyEnabled: !!serverInfo?.postgres_proxy_enabled,
      applicationMode: serverInfo?.application_mode || null,
      adminRoleName: serverInfo?.admin_role_name || ROLE_ADMIN,
      approverRoleName: serverInfo?.approver_role_name || ROLE_APPROVER
    })
  },
  setFeatureFlags: (flags) => set({ featureFlags: flags }),
  isFeatureFlagEnabled: (name) => !!get().featureFlags?.[name],
  isLicenseFeatureEnabled: (name) => {
    const { serverInfoLoaded, licenseFeatures } = get()
    // Unknown license state (serverinfo missing or failed) never grants
    // access to a gated feature.
    if (!serverInfoLoaded) return false
    return !licenseFeatures?.length || licenseFeatures.includes(name)
  },
  setLoading: (loading) => set({ loading }),
  clear: () => {
    if (window.Intercom) window.Intercom('shutdown')
    set({ 
      user: null,
      role: ROLE_STANDARD,
      isAdmin: false,
      isApprover: false,
      isSelfHosted: false,
      isFreeLicense: true, 
      analyticsTracking: false, 
      analyticsMode: 'anonymous', 
      disableClipboard: false, 
      gatewayVersion: null, 
      featureFlags: {},
      licenseFeatures: null,
      licenseInfo: null,
      serverInfoLoaded: false,
      redactProvider: null, 
      apiUrl: null,
      hasRedactCredentials: false,
      postgresProxyEnabled: false,
    })
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
      // The "Contact support" item in the header user menu carries this id.
      // Without the selector the item is inert on React-only routes, where the
      // CLJS boot (webapp/src/webapp/events.cljs) never runs to register it.
      custom_launcher_selector: '#intercom-support-trigger',
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

  // Opens the Intercom messenger with a prefilled message, booting it first
  // when the app-boot initialization was skipped or shut down (the CLJS boot
  // races gateway info on load and can leave the messenger unbooted, which
  // renders as a blank white window). Returns false when Intercom is
  // unavailable so callers can fall back to the sales page.
  showIntercomMessage: (message) => {
    const { analyticsTracking, user } = get()
    if (!analyticsTracking) return false
    if (!window.Intercom?.booted) get().initIntercom(user)
    if (!window.Intercom) return false
    window.Intercom('showNewMessage', message)
    return true
  },

  initAnalytics: (user) => {
    const { analyticsTracking, analyticsMode } = get()
    if (!analyticsTracking) return
    analyticsIdentify(user, analyticsMode).catch((err) => {
      console.warn('[analytics] identify failed:', err)
    })
  },
}))
