(ns webapp.connections.views.setup.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.views.setup.database :as database]
   [webapp.connections.views.setup.network :as network]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]
   [webapp.connections.views.setup.server :as server]
   [webapp.connections.views.setup.state :as state]
   [webapp.connections.views.setup.type-selector :as type-selector]))

(defn main [form-type initial-data]
  (let [connection-type (rf/subscribe [:connection-setup/connection-type])]
    (fn [form-type initial-data]
      (let [state (state/create-initial-state form-type initial-data)]
        [page-wrapper/main
         {:children [:> Box {:class "min-h-screen bg-gray-1"}
                           ;; Main content
                     (case @connection-type
                       "database" [database/main]
                       "server-container" [server/main]
                       "network" [network/main]
                       [type-selector/main])]
          :footer-props {:next-hidden? true
                         :hide-footer? (boolean @connection-type)}}]))))
