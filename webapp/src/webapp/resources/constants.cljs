(ns webapp.resources.constants)

;; Role configuration fields for different types
(def role-configs-required
  {:database/postgres [{:key "host" :label "Host" :value "" :required true}
                       {:key "user" :label "User" :value "" :required true}
                       {:key "pass" :label "Pass" :value "" :required true}
                       {:key "port" :label "Port" :value "" :required true}
                       {:key "db" :label "Db" :value "" :required true}
                       {:key "sslmode" :label "Sslmode (Optional)" :value "" :required false}]
   :database/mysql [{:key "host" :label "Host" :value "" :required true}
                    {:key "user" :label "User" :value "" :required true}
                    {:key "pass" :label "Pass" :value "" :required true}
                    {:key "port" :label "Port" :value "" :required true}
                    {:key "db" :label "Db" :value "" :required true}]
   :database/mongodb [{:key "connection_string"
                       :label "Connection string"
                       :value ""
                       :required true
                       :placeholder "mongodb+srv://root:<password>@devcluster.mwb5sun.mongodb.net/"}]
   :database/mssql [{:key "host" :label "Host" :value "" :required true}
                    {:key "user" :label "User" :value "" :required true}
                    {:key "pass" :label "Pass" :value "" :required true}
                    {:key "port" :label "Port" :value "" :required true}
                    {:key "db" :label "Db" :value "" :required true}
                    {:key "insecure" :label "Insecure (Optional)" :value "false" :required false}]
   :database/oracledb [{:key "host" :label "Host" :value "" :required true}
                       {:key "user" :label "User" :value "" :required true}
                       {:key "pass" :label "Pass" :value "" :required true}
                       {:key "port" :label "Port" :value "" :required true}
                       {:key "sid" :placeholder "SID or Service name" :label "SID" :value "" :required true}]
   :application/ssh [{:key "host" :label "Host" :value "" :required true}
                     {:key "port" :label "Port" :value "" :required false}
                     {:key "user" :label "User" :value "" :required true}
                     {:key "pass" :label "Pass" :value "" :required false}
                     {:key "authorized_server_keys"
                      :label "Private Key"
                      :value ""
                      :required false
                      :placeholder "Enter your private key"
                      :type "textarea"}]
   :application/tcp [{:key "host" :label "Host" :value "" :required true}
                     {:key "port" :label "Port" :value "" :required true}]
   :application/httpproxy [{:key "remote_url" :label "Remote URL" :value "" :required true}
                           {:key "insecure" :label "Insecure" :value "false" :required false :type "checkbox"}]})

;; Get role config based on type and subtype
(defn get-role-config [type subtype]
  (get role-configs-required (keyword (str type "/" subtype))))
