(ns webapp.connections.views.setup.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.views.setup.database :as database]
   [webapp.connections.views.setup.server :as server]
   [webapp.connections.views.setup.state :as state]
   [webapp.connections.views.setup.type-selector :as type-selector]))

(defn header-back []
  [:> Flex {:p "5" :gap "2"}
   [:> Button {:variant "ghost"
               :size "2"
               :color "gray"
               :on-click #(rf/dispatch [:connection-setup/go-back])}
    "Back"]])

(defn setup-flow []
  (let [connection-type @(rf/subscribe [:connection-setup/connection-type])
        current-step @(rf/subscribe [:connection-setup/current-step])]
    [:> Box {:class "min-h-screen bg-gray-1"}
     [header-back]

     ;; Main content
     (case connection-type
       "database" [database/main]
       "server-container" [server/main]
       ;; "network" [network/main]
       [type-selector/main])]))

(defn main [form-type initial-data]
  (let [scroll-pos (r/atom 0)
        handle-scroll #(reset! scroll-pos (.-scrollY js/window))]

    (r/create-class
     {:display-name "connection-setup"

      :component-did-mount
      (fn [_]
        (.addEventListener js/window "scroll" handle-scroll))

      :component-will-unmount
      (fn [_]
        (.removeEventListener js/window "scroll" handle-scroll))

      :reagent-render
      (fn [form-type initial-data]
        (let [state (state/create-initial-state form-type initial-data)]
          [setup-flow]))})))
