(ns webapp.events.tracking
  (:require
   ["@sentry/browser" :as Sentry]
   [re-frame.core :as rf]
   [webapp.config :as config]))

(rf/reg-event-fx
 :tracking->initialize-if-allowed
 (fn
   [_ _]
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
 :tracking->load-scripts
 (fn
   [_ _]
   ;; Only inject scripts if they don't already exist
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
