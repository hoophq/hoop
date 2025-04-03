(ns webapp.core
  (:require
   [reagent.dom :as rdom]
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

(defn add-global-keyboard-shortcuts []
  (.addEventListener js/document "keydown" 
    (fn [e]
      (when (and (or (.-metaKey e) (.-ctrlKey e)) 
                 (= (.-key e) "k"))
        (.preventDefault e)
        (let [search-input (.getElementById js/document "header-search")]
          (when search-input
            (.focus search-input)))))))

(defn init []
  (routes/start!)
  (re-frame/dispatch-sync [::events/initialize-db])
  (dev-setup)
  (add-global-keyboard-shortcuts)
  (mount-root))
