(ns webapp.connections.constants
  (:require [clojure.string :as cs]
            [webapp.config :as config]))

(def connection-configs-required
  {:command-line []
   :custom []
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
   :ssh [{:key "ssh_uri" :value "" :required true :placeholder "ssh://uri"}]})

(def connection-icons-name-dictionary
  {:dark {:postgres (str config/webapp-url "/images/connections-logos/postgres_logo.svg")
          :postgres-csv (str config/webapp-url "/images/connections-logos/postgres_logo.svg")
          :command-line (str config/webapp-url "/images/connections-logos/dark/custom_dark.svg")
          :ssh (str config/webapp-url "/images/connections-logos/dark/custom_dark.svg")
          :custom (str config/webapp-url "/images/connections-logos/dark/custom_dark.svg")
          :tcp (str config/webapp-url "/images/connections-logos/dark/tcp_dark.svg")
          :mysql (str config/webapp-url "/images/connections-logos/dark/mysql_dark.png")
          :mysql-csv (str config/webapp-url "/images/connections-logos/dark/mysql_dark.png")
          :aws (str config/webapp-url "/images/connections-logos/aws_logo.svg")
          :bastion (str config/webapp-url "/images/connections-logos/bastion_logo.svg")
          :heroku (str config/webapp-url "/images/connections-logos/heroku_logo.svg")
          :nodejs (str config/webapp-url "/images/connections-logos/node_logo.svg")
          :python (str config/webapp-url "/images/connections-logos/python_logo.svg")
          :ruby-on-rails (str config/webapp-url "/images/connections-logos/dark/rails_dark.svg")
          :clojure (str config/webapp-url "/images/connections-logos/clojure_logo.svg")
          :kubernetes (str config/webapp-url "/images/connections-logos/k8s_logo.svg")
          :sql-server-csv (str config/webapp-url "/images/connections-logos/sql-server_logo.svg")
          :sql-server (str config/webapp-url "/images/connections-logos/sql-server_logo.svg")
          :oracledb (str config/webapp-url "/images/connections-logos/oracle_logo.svg")
          :mssql (str config/webapp-url "/images/connections-logos/sql-server_logo.svg")
          :mongodb (str config/webapp-url "/images/connections-logos/mongodb_logo.svg")}
   :light {:postgres (str config/webapp-url "/images/connections-logos/postgres_logo.svg")
           :postgres-csv (str config/webapp-url "/images/connections-logos/postgres_logo.svg")
           :command-line (str config/webapp-url "/images/connections-logos/command-line.svg")
           :ssh (str config/webapp-url "/images/connections-logos/command-line.svg")
           :custom (str config/webapp-url "/images/connections-logos/command-line.svg")
           :tcp (str config/webapp-url "/images/connections-logos/tcp_logo.svg")
           :mysql (str config/webapp-url "/images/connections-logos/mysql_logo.png")
           :mysql-csv (str config/webapp-url "/images/connections-logos/mysql_logo.png")
           :aws (str config/webapp-url "/images/connections-logos/aws_logo.svg")
           :bastion (str config/webapp-url "/images/connections-logos/bastion_logo.svg")
           :heroku (str config/webapp-url "/images/connections-logos/heroku_logo.svg")
           :nodejs (str config/webapp-url "/images/connections-logos/node_logo.svg")
           :python (str config/webapp-url "/images/connections-logos/python_logo.svg")
           :ruby-on-rails (str config/webapp-url "/images/connections-logos/rails_logo.svg")
           :clojure (str config/webapp-url "/images/connections-logos/clojure_logo.svg")
           :kubernetes (str config/webapp-url "/images/connections-logos/k8s_logo.svg")
           :sql-server-csv (str config/webapp-url "/images/connections-logos/sql-server_logo.svg")
           :sql-server (str config/webapp-url "/images/connections-logos/sql-server_logo.svg")
           :oracledb (str config/webapp-url "/images/connections-logos/oracle_logo.svg")
           :mssql (str config/webapp-url "/images/connections-logos/sql-server_logo.svg")
           :mongodb (str config/webapp-url "/images/connections-logos/mongodb_logo.svg")}})

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
   "custom" ""
   "ssh" "ssh $SSH_URI -i $SSH_PRIVATE_KEY"
   "" ""})

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
            :envvar:HOST "ZGVtby1wZy1kYi5jaDcwN3JuYWl6amcudXMtZWFzdC0xLnJkcy5hbWF6b25hd3MuY29t"
            :envvar:INSECURE "ZmFsc2U="
            :envvar:PASS "ZG9sbGFyLW1hbmdlci1jYXJvdXNlLUhFQVJURUQ="
            :envvar:PORT "NTQzMg=="
            :envvar:USER "ZGVtb3JlYWRvbmx5"}
   :command []})
