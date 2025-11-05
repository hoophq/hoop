(ns webapp.resources.views.configure-role.credentials-tab
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [webapp.connections.views.setup.database :as database]
   [webapp.connections.views.setup.network :as network]
   [webapp.connections.views.setup.server :as server]))

(defn main [connection]
  [:> Box {:class "max-w-[600px] space-y-8"}
   (case (:type connection)
     "database" [database/credentials-step (:subtype connection) :update]
     "custom" [server/credentials-step :update]
     "application" (if (= (:subtype connection) "ssh")
                     [server/ssh-credentials]
                     [network/credentials-form
                      {:connection-type (:subtype connection)}])
     nil)])

