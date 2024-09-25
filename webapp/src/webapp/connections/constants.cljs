(ns webapp.connections.constants
  (:require [clojure.string :as cs]))

(def connection-configs-required
  {:command-line []
   :tcp [{:key "host" :value "" :required true}
         {:key "port" :value "" :required true}]
   :mysql [{:key "host" :value "" :required true}
           {:key "user" :value "" :required true}
           {:key "pass" :value "" :required true}
           {:key "port" :value "" :required true}
           {:key "db" :value "" :required true}]
   :postgres [{:key "host" :value "" :required true}
              {:key "user" :value "" :required true}
              {:key "pass" :value "" :required true}
              {:key "port" :value "" :required true}
              {:key "db" :value "" :required true}
              {:key "sslmode" :value "" :required false}]
   :mssql [{:key "host" :value "" :required true}
           {:key "user" :value "" :required true}
           {:key "pass" :value "" :required true}
           {:key "port" :value "" :required true}
           {:key "db" :value "" :required true}
           {:key "insecure" :value "false" :required false}]
   :oracledb [{:key "host" :value "" :required true}
              {:key "user" :value "" :required true}
              {:key "pass" :value "" :required true}
              {:key "port" :value "" :required true}
              {:key "ld_library_path" :value "/opt/oracle/instantclient_19_24" :hidden true :required true}
              {:key "sid" :placeholder "SID or Service name" :value "" :required true}]
   :mongodb [{:key "connection_string"
              :value ""
              :required true
              :placeholder "mongodb+srv://root:<password>@devcluster.mwb5sun.mongodb.net/"}]
   :ssh [{:key "ssh_uri" :value "" :required true :placeholder "ssh://user@host"}]})

(def connection-icons-name-dictionary
  {:dark {:postgres "/images/connections-logos/postgres_logo.svg"
          :postgres-csv "/images/connections-logos/postgres_logo.svg"
          :command-line "/images/connections-logos/dark/custom_dark.svg"
          :ssh "/images/connections-logos/dark/custom_dark.svg"
          :custom "/images/connections-logos/dark/custom_dark.svg"
          :tcp "/images/connections-logos/dark/tcp_dark.svg"
          :mysql "/images/connections-logos/dark/mysql_dark.png"
          :mysql-csv "/images/connections-logos/dark/mysql_dark.png"
          :aws "/images/connections-logos/aws_logo.svg"
          :bastion "/images/connections-logos/bastion_logo.svg"
          :heroku "/images/connections-logos/heroku_logo.svg"
          :nodejs "/images/connections-logos/node_logo.svg"
          :python "/images/connections-logos/python_logo.svg"
          :ruby-on-rails "/images/connections-logos/dark/rails_dark.svg"
          :clojure "/images/connections-logos/clojure_logo.svg"
          :kubernetes "/images/connections-logos/k8s_logo.svg"
          :sql-server-csv "/images/connections-logos/sql-server_logo.svg"
          :sql-server "/images/connections-logos/sql-server_logo.svg"
          :oracledb "/images/connections-logos/oracle_logo.svg"
          :mssql "/images/connections-logos/sql-server_logo.svg"
          :mongodb "/images/connections-logos/mongodb_logo.svg"}
   :light {:postgres "/images/connections-logos/postgres_logo.svg"
           :postgres-csv "/images/connections-logos/postgres_logo.svg"
           :command-line "/images/connections-logos/command-line.svg"
           :ssh "/images/connections-logos/command-line.svg"
           :custom "/images/connections-logos/command-line.svg"
           :tcp "/images/connections-logos/tcp_logo.svg"
           :mysql "/images/connections-logos/mysql_logo.png"
           :mysql-csv "/images/connections-logos/mysql_logo.png"
           :aws "/images/connections-logos/aws_logo.svg"
           :bastion "/images/connections-logos/bastion_logo.svg"
           :heroku "/images/connections-logos/heroku_logo.svg"
           :nodejs "/images/connections-logos/node_logo.svg"
           :python "/images/connections-logos/python_logo.svg"
           :ruby-on-rails "/images/connections-logos/rails_logo.svg"
           :clojure "/images/connections-logos/clojure_logo.svg"
           :kubernetes "/images/connections-logos/k8s_logo.svg"
           :sql-server-csv "/images/connections-logos/sql-server_logo.svg"
           :sql-server "/images/connections-logos/sql-server_logo.svg"
           :oracledb "/images/connections-logos/oracle_logo.svg"
           :mssql "/images/connections-logos/sql-server_logo.svg"
           :mongodb "/images/connections-logos/mongodb_logo.svg"}})

(defn get-connection-icon [connection & [theme]]
  (let [connection-icons (get connection-icons-name-dictionary (or theme :light))]
    (cond
      (not (cs/blank? (:subtype connection))) (get connection-icons (keyword (:subtype connection)))
      (not (cs/blank? (:icon_name connection))) (get connection-icons (keyword (:icon_name connection)))
      :else (get connection-icons (keyword (:type connection))))))

(def connection-commands
  {"nodejs" "node"
   "clojure" "clj"
   "python" "python3"
   "ruby-on-rails" "rails runner -"
   "postgres" ""
   "mysql" ""
   "mssql" ""
   "mongodb" ""
   "ssh" "ssh $SSH_URI -i $SSH_PRIVATE_KEY"})

(def connection-postgres-demo
  {:name "postgres-demo"
   :type "database"
   :subtype "postgres"
   :access_mode_runbooks "enabled"
   :access_mode_exec "enabled"
   :access_mode_connect "enabled"
   :access_schema "enabled"
   :agent_id ""
   :reviewers []
   :redact_enabled false
   :redact_types []
   :secret {:envvar:DB "ZGVsbHN0b3Jl"
            :envvar:DBNAME "ZGVsbHN0b3Jl"
            :envvar:HOST "ZGVtby1wZy1kYi5jaDcwN3JuYWl6amcudXMtZWFzdC0xLnJkcy5hbWF6b25hd3MuY29t"
            :envvar:INSECURE "ZmFsc2U="
            :envvar:PASS "ZG9sbGFyLW1hbmdlci1jYXJvdXNlLUhFQVJURUQ="
            :envvar:PORT "NTQzMg=="
            :envvar:USER "ZGVtb3JlYWRvbmx5"}
   :command []})
