(ns webapp.events.tracking
  (:require
   ["@sentry/browser" :as Sentry]
   [re-frame.core :as rf]
   [webapp.config :as config]))

(defn- create-script-element [src id async defer]
  (let [script-element (.createElement js/document "script")]
    (when id (.setAttribute script-element "id" id))
    (.setAttribute script-element "src" src)
    (.setAttribute script-element "type" "text/javascript")
    (when async (.setAttribute script-element "async" "true"))
    (when defer (.setAttribute script-element "defer" "true"))
    script-element))

(defn- inject-script [src id async defer]
  (let [script-element (create-script-element src id async defer)
        head-element (.getElementsByTagName js/document "head")
        head (aget head-element 0)]
    (.appendChild head script-element)))

(rf/reg-event-fx
 :tracking->initialize-if-allowed
 (fn
   [{:keys [db]} _]
   (let [analytics-tracking @(rf/subscribe [:gateway->analytics-tracking])]
     (if (not analytics-tracking)
       ;; Tracking is disabled, ensure all tracking is stopped
       {:fx [[:dispatch [:tracking->disable-all-tracking]]]}
       ;; Tracking is allowed, initialize all tracking components
       {:fx [[:dispatch [:tracking->load-scripts]]
             [:dispatch [:segment->load]]]}))))

(rf/reg-event-fx
 :tracking->disable-all-tracking
 (fn [_ _]
   ;; Disable Sentry if it was initialized
   (when (exists? js/Sentry)
     ;; Try to close the Sentry client
     (try
       (when-let [hub (.-getCurrentHub js/Sentry)]
         (when-let [client (.getClient hub)]
           (when-let [close (.-close client)]
             (.close client))))
       (catch :default e
         (js/console.warn "Error trying to close Sentry:", e)))

     ;; Also try to cancel all future transmissions
     (try
       (when (exists? js/Sentry.configureScope)
         (.configureScope js/Sentry
                          (fn [scope]
                            (.setUser scope nil)
                            (.clear scope))))
       (catch :default _ nil)))

   ;; Disable Segment if it was initialized
   (when (exists? js/analytics)
     (try
       (when (exists? (.-reset js/analytics))
         (.reset js/analytics))
       (catch :default _ nil)))

   {}))

(rf/reg-event-fx
 :tracking->ensure-canny-available
 (fn [{:keys [db]} [_ user-data]]
   (let [analytics-tracking @(rf/subscribe [:gateway->analytics-tracking])]
     (if (not analytics-tracking)
       ;; Tracking is disabled, do nothing
       {}
       ;; Otherwise, identify user in Canny if available
       (do
         (when (and (exists? js/Canny) (= (type js/Canny) js/Function))
           (js/Canny "identify"
                     #js{:appID config/canny-id
                         :user #js{:email (:email user-data)
                                   :name (:name user-data)
                                   :id (:id user-data)}}))
         {})))))

(rf/reg-event-fx
 :tracking->load-scripts
 (fn
   [_ _]
   ;; Only inject scripts if they don't already exist
   (when-not (.getElementById js/document "google-tag-manager")
     (inject-script "https://www.googletagmanager.com/gtag/js?id=G-ZS8J67B1SX" "google-tag-manager" true false)
     (let [gtag-script (.createElement js/document "script")]
       (.setAttribute gtag-script "type" "text/javascript")
       (set! (.-innerHTML gtag-script) "window.dataLayer = window.dataLayer || []; function gtag() { dataLayer.push(arguments); } gtag('js', new Date()); gtag('config', 'G-ZS8J67B1SX');")
       (let [head-element (.getElementsByTagName js/document "head")
             head (aget head-element 0)]
         (.appendChild head gtag-script))))

   (when-not (.getElementById js/document "paddle-js")
     (inject-script "https://cdn.paddle.com/paddle/v2/paddle.js" "paddle-js" true false)
     (let [paddle-script (.createElement js/document "script")]
       (.setAttribute paddle-script "type" "text/javascript")
       (set! (.-innerHTML paddle-script) "Paddle.Initialize({ token: 'live_fb0003b8e4345ffca9e35f0e34b', pwCustomer: {} });")
       (let [head-element (.getElementsByTagName js/document "head")
             head (aget head-element 0)]
         (.appendChild head paddle-script))))

   (when-not (.getElementById js/document "clarity-script")
     (let [clarity-script (.createElement js/document "script")]
       (.setAttribute clarity-script "id" "clarity-script")
       (.setAttribute clarity-script "type" "text/javascript")
       (set! (.-innerHTML clarity-script)
             (str
              ;; We don't define window.clarity directly to avoid recursion
              "(function (c, l, a, r, i, t, y) { "
              "  c[a] = c[a] || function () { (c[a].q = c[a].q || []).push(arguments) }; "
              "  t = l.createElement(r); t.async = 1; t.src = \"https://www.clarity.ms/tag/\" + i; "
              "  y = l.getElementsByTagName(r)[0]; y.parentNode.insertBefore(t, y); "
              "})(window, document, \"clarity\", \"script\", \"h9osgp95be\");"))
       (let [head-element (.getElementsByTagName js/document "head")
             head (aget head-element 0)]
         (.appendChild head clarity-script))))

   (when-not (.getElementById js/document "intercom-script")
     (let [intercom-script (.createElement js/document "script")]
       (.setAttribute intercom-script "id" "intercom-script")
       (.setAttribute intercom-script "type" "text/javascript")
       (set! (.-innerHTML intercom-script) "(function () { var w = window; var ic = w.Intercom; if (typeof ic === \"function\") { ic('reattach_activator'); ic('update', w.intercomSettings); } else { var d = document; var i = function () { i.c(arguments); }; i.q = []; i.c = function (args) { i.q.push(args); }; w.Intercom = i; var l = function () { var s = d.createElement('script'); s.type = 'text/javascript'; s.async = true; s.src = 'https://widget.intercom.io/widget/ryuapdmp'; var x = d.getElementsByTagName('script')[0]; x.parentNode.insertBefore(s, x); }; if (w.attachEvent) { w.attachEvent('onload', l); } else { w.addEventListener('load', l, false); } } })();")
       (let [head-element (.getElementsByTagName js/document "head")
             head (aget head-element 0)]
         (.appendChild head intercom-script))))

   (when-not (.getElementById js/document "canny-sdk")
     (let [canny-script (.createElement js/document "script")]
       (.setAttribute canny-script "id" "canny-sdk")
       (.setAttribute canny-script "type" "text/javascript")
       (set! (.-innerHTML canny-script) "!function (w, d, i, s) { function l() { if (!d.getElementById(i)) { var f = d.getElementsByTagName(s)[0], e = d.createElement(s); e.type = \"text/javascript\", e.async = !0, e.src = \"https://canny.io/sdk.js\", f.parentNode.insertBefore(e, f) } } if (\"function\" != typeof w.Canny) { var c = function () { c.q.push(arguments) }; c.q = [], w.Canny = c, \"complete\" === d.readyState ? l() : w.attachEvent ? w.attachEvent(\"onload\", l) : w.addEventListener(\"load\", l, !1) } }(window, document, \"canny-jssdk\", \"script\");")
       (let [head-element (.getElementsByTagName js/document "head")
             head (aget head-element 0)]
         (.appendChild head canny-script))))

   {}))

(rf/reg-event-fx
 :initialize-monitoring
 (fn [{:keys [db]} _]
   (let [sentry-dsn config/sentry-dsn
         sentry-sample-rate config/sentry-sample-rate
         analytics-tracking (= "enabled" (get-in db [:gateway->info :data :analytics_tracking] "disabled"))]
     (when (and sentry-dsn sentry-sample-rate (not analytics-tracking))
       (try
         (.init Sentry #js {:dsn sentry-dsn
                            :release config/app-version
                            :sampleRate sentry-sample-rate
                            :integrations #js [(.browserTracingIntegration js/Sentry)]})
         (catch :default e
           (js/console.error "Failed to initialize Sentry:" e)))))
   {}))
