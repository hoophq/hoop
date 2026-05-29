(ns webapp.resources.configure-role.credentials-tab
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [re-frame.core :as rf]
   [webapp.connections.views.setup.connection-method :as connection-method]
   [webapp.connections.views.setup.metadata-driven :as metadata-driven]
   [webapp.connections.views.setup.network :as network]
   [webapp.connections.views.setup.server :as server]
   [webapp.resources.configure-role.claude-code-edit :as claude-code-edit]
   [webapp.resources.federation.views.setup :as federation-setup]))

(defn bigquery-credentials [connection]
  (let [method (or @(rf/subscribe [:connection-setup/connection-method]) "manual-input")]
    [:> Box {:class "space-y-8"}
     [connection-method/main "bigquery"]
     (if (= method "iam_federation")
       [federation-setup/main {:connection-name (:name connection)
                               :conn-data connection}]
       [:> Box {:class "max-w-[600px]"}
        [metadata-driven/credentials-step "bigquery" :update]])]))

(defn main [connection]
  (if (and (= (:type connection) "database")
           (= (:subtype connection) "bigquery"))
    [bigquery-credentials connection]

    [:> Box {:class "max-w-[600px] space-y-8"}
     (case (:type connection)
       "database" [metadata-driven/credentials-step (:subtype connection) :update]

       "custom" (let [subtype (:subtype connection)]
                  (cond
                    (= subtype "kubernetes-token") [server/kubernetes-token]
                    (and subtype (not (contains? #{"tcp" "httpproxy" "ssh" "linux-vm" "claude-code"} subtype)))
                    [metadata-driven/credentials-step subtype :update]
                    :else
                    [server/credentials-step :update]))

       "httpproxy" (let [subtype (:subtype connection)]
                     (cond
                       (= subtype "claude-code") [claude-code-edit/claude-code-edit-form]
                       :else [network/http-credentials-form]))

       "application" (if (or (= (:subtype connection) "ssh")
                             (= (:subtype connection) "git")
                             (= (:subtype connection) "github"))
                       [server/ssh-credentials]
                       [network/credentials-form
                        {:connection-type (:subtype connection)}])
       nil)]))
