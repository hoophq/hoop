(ns webapp.connections.constants
  (:require [clojure.string :as cs]))

(def connection-configs-required
  {:command-line []
   :tcp [{:key "host" :value ""}
         {:key "port" :value ""}]
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
   :mongodb [{:key "connection_string"
              :value ""
              :required true
              :placeholder "mongodb+srv://root:<password>@devcluster.mwb5sun.mongodb.net/"}]})

(def connection-icons-name-dictionary
  {:postgres "/images/connections-logos/postgres_logo.svg"
   :postgres-csv "/images/connections-logos/postgres_logo.svg"
   :command-line "/images/connections-logos/command-line.svg"
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
   :mssql "/images/connections-logos/sql-server_logo.svg"
   :mongodb "/images/connections-logos/mongodb_logo.svg"})

(defn get-connection-icon [connection]
  (cond
    (not (cs/blank? (:subtype connection))) (get connection-icons-name-dictionary (keyword (:subtype connection)))
    (not (cs/blank? (:icon_name connection))) (get connection-icons-name-dictionary (keyword (:icon_name connection)))
    :else (get connection-icons-name-dictionary (keyword (:type connection)))))

(def connection-commands
  {"nodejs" "node"
   "clojure" "clj"
   "python" "python3"
   "ruby-on-rails" "rails runner -"
   "postgres" ""
   "mysql" ""
   "mssql" ""
   "mongodb" ""})

(def connection-postgres-demo
  {:name "postgres-demo"
   :type "database"
   :subtype "postgres"
   :agent_id ""
   :reviewers []
   :redact_enabled true
   :redact_types ["PHONE_NUMBER",
                  "CREDIT_CARD_NUMBER",
                  "CREDIT_CARD_TRACK_NUMBER",
                  "EMAIL_ADDRESS",
                  "IBAN_CODE",
                  "HTTP_COOKIE",
                  "IMEI_HARDWARE_ID",
                  "IP_ADDRESS",
                  "STORAGE_SIGNED_URL",
                  "URL",
                  "VEHICLE_IDENTIFICATION_NUMBER",
                  "BRAZIL_CPF_NUMBER",
                  "AMERICAN_BANKERS_CUSIP_ID",
                  "FDA_CODE",
                  "US_PASSPORT",
                  "US_SOCIAL_SECURITY_NUMBER"]
   :secret {:envvar:DB "ZGVsbHN0b3Jl"
            :envvar:DBNAME "ZGVsbHN0b3Jl"
            :envvar:HOST "ZGVtby1wZy1kYi5jaDcwN3JuYWl6amcudXMtZWFzdC0xLnJkcy5hbWF6b25hd3MuY29t"
            :envvar:INSECURE "ZmFsc2U="
            :envvar:PASS "ZG9sbGFyLW1hbmdlci1jYXJvdXNlLUhFQVJURUQ="
            :envvar:PORT "NTQzMg=="
            :envvar:USER "ZGVtb3JlYWRvbmx5"}
   :command []})
