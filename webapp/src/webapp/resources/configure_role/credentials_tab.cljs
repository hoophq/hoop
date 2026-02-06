(ns webapp.resources.configure-role.credentials-tab
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [webapp.connections.views.setup.metadata-driven :as metadata-driven]
   [webapp.connections.views.setup.network :as network]
   [webapp.connections.views.setup.server :as server]
   [webapp.connections.views.setup.claude-code :as claude-code]))

(defn main [connection]
  [:> Box {:class "max-w-[600px] space-y-8"}
   (case (:type connection)
     "database" [metadata-driven/credentials-step (:subtype connection) :update]

     "custom" (let [subtype (:subtype connection)]
                (cond
                  (= subtype "kubernetes-token") [server/kubernetes-token]
                  (= subtype "claude-code") [claude-code/claude-code-credentials-form]
                  (and subtype (not (contains? #{"tcp" "httpproxy" "ssh" "linux-vm" "claude-code"} subtype)))
                  [metadata-driven/credentials-step subtype :update]
                  :else
                  [server/credentials-step :update]))

     "httpproxy" [network/http-credentials-form]

     "application" (if (or (= (:subtype connection) "ssh")
                           (= (:subtype connection) "git")
                           (= (:subtype connection) "github"))
                     [server/ssh-credentials]
                     [network/credentials-form
                      {:connection-type (:subtype connection)}])
     nil)])

