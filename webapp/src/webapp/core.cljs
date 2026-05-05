(ns webapp.core
  (:require
   [reagent.dom :as rdom]
   [re-frame.core :as re-frame]
   [webapp.events :as events]
   [webapp.routes :as routes]
   [webapp.app :as views]
   [webapp.config :as config]
   [webapp.events.jobs]
   [webapp.parallel-mode.core]))

(defn dev-setup []
  (when config/debug?
    (println "dev mode")))

(defn ^:dev/after-load mount-root []
  (re-frame/clear-subscription-cache!)
  (when-let [root-el (.getElementById js/document "app")]
    (rdom/unmount-component-at-node root-el)
    (rdom/render [views/main-panel] root-el)))

(defn init []
  (routes/start!)
  (re-frame/dispatch-sync [::events/initialize-db])
  (dev-setup)
  (mount-root)
  ;; Expose functions for the React shell (microfrontend)
  (set! (.-hoopRemount js/window) mount-root)
  ;; Set active panel directly from a URL path (no pushState side-effect)
  (set! (.-hoopSetRoute js/window)
        (fn [path]
          (let [route (routes/parse path)
                panel (keyword (str (name (:handler route)) "-panel"))]
            (re-frame/dispatch-sync [::events/set-active-panel panel]))))
  ;; Generic Re-frame dispatch bridge for the React shell.
  ;; Call from React via: window.hoopDispatch(["event-name", ...args])
  ;; First element is the event keyword (string → keyword conversion is automatic).
  ;; Remove when CLJS is fully gone.
  (set! (.-hoopDispatch js/window)
        (fn [js-event-vec]
          (let [clj-vec  (js->clj js-event-vec :keywordize-keys true)
                event-kw (keyword (first clj-vec))
                args     (rest clj-vec)]
            (re-frame/dispatch (into [event-kw] args))))))
