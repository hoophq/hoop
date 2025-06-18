(ns webapp.core
  (:require
   [reagent.dom.client :as rdom]
   [re-frame.core :as re-frame]
   [webapp.events :as events]
   [webapp.routes :as routes]
   [webapp.app :as views]
   [webapp.config :as config]
   [webapp.events.jobs]))

(defn dev-setup []
  (when config/debug?
    (println "dev mode")))

(defn ^:dev/after-load mount-root []
  (re-frame/clear-subscription-cache!)
  (let [root-el (.getElementById js/document "app")]
    (rdom/unmount-component-at-node root-el)
    (rdom/render [views/main-panel] root-el)))

(defn init []
  (routes/start!)
  (re-frame/dispatch-sync [::events/initialize-db])
  (dev-setup)
  (mount-root))
