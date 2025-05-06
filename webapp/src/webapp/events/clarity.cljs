(ns webapp.events.clarity
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :clarity->verify-environment
 (fn [{:keys [db]} [_ user]]
   (let [analytics-tracking @(rf/subscribe [:gateway->analytics-tracking])
         hostname (.-hostname js/window.location)
         clarity-available? (and (exists? js/window.clarity)
                                 (= (type js/window.clarity) js/Function))]
     (if (not analytics-tracking)
       ;; If analytics tracking is disabled, always stop clarity
       (try
         (when clarity-available?
           (.clarity js/window "stop"))
         (catch :default _ nil))
       ;; Otherwise, apply normal logic
       (try
         (when clarity-available?
           (cond
             (= hostname "localhost") (.clarity js/window "stop")
             (= hostname "127.0.0.1") (.clarity js/window "stop")
             (= hostname "appdemo.hoop.dev") (.clarity js/window "stop")
             (= hostname "tryrunops.hoop.dev") (.clarity js/window "stop")
             (= (:org_name user) "hoopdev") (.clarity js/window "stop")
             :else (.clarity js/window "start")))
         (catch :default _ nil))))
   {}))
