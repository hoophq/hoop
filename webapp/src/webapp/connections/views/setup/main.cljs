(ns webapp.connections.views.setup.main
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [re-frame.core :as rf]
   [webapp.connections.views.setup.database :as database]
   [webapp.connections.views.setup.metadata-driven :as metadata-driven]
   [webapp.connections.views.setup.network :as network]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]
   [webapp.connections.views.setup.server :as server]
   [webapp.connections.views.setup.type-selector :as type-selector]))

(defn main [_]
  (let [connection-type (rf/subscribe [:connection-setup/connection-type])]
    (fn [form-type]
      [page-wrapper/main
       {:children [:> Box {:class "min-h-screen bg-gray-1"}
                   ;; Main content - pula type selector se veio do catálogo
                   (if @connection-type
                     ;; Se há connection-type (normal ou do catálogo), vai direto
                     (case @connection-type
                       "database" [database/main form-type]
                       "server" [server/main form-type]
                       "network" [network/main form-type]
                       "custom" [metadata-driven/main form-type]
                       [type-selector/main form-type])
                     ;; Senão, mostra type selector
                     [type-selector/main form-type])]
        :footer-props {:form-type form-type
                       :next-hidden? true
                       :hide-footer? (boolean @connection-type)}}])))
