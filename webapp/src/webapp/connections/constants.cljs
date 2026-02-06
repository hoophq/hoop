(ns webapp.connections.constants
  (:require [clojure.string :as cs]
            [webapp.config :as config]))

(def connection-configs-required
  {:command-line []
   :custom []
   :tcp [{:key "host" :label "Host" :value "" :required true}
         {:key "port" :label "Port" :value "" :required true}]
   :mysql [{:key "host" :label "Host" :value "" :required true}
           {:key "user" :label "User" :value "" :required true}
           {:key "pass" :label "Pass" :value "" :required true}
           {:key "port" :label "Port" :value "" :required true}
           {:key "db" :label "Db" :value "" :required true}]
   :postgres [{:key "host" :label "Host" :value "" :required true}
              {:key "user" :label "User" :value "" :required true}
              {:key "pass" :label "Pass" :value "" :required true}
              {:key "port" :label "Port" :value "" :required true}
              {:key "db" :label "Db" :value "" :required true}
              {:key "sslmode" :label "Sslmode (Optional)" :value "" :required false}]
   :mssql [{:key "host" :label "Host" :value "" :required true}
           {:key "user" :label "User" :value "" :required true}
           {:key "pass" :label "Pass" :value "" :required true}
           {:key "port" :label "Port" :value "" :required true}
           {:key "db" :label "Db" :value "" :required true}
           {:key "insecure" :label "Insecure (Optional)" :value "false" :required false}]
   :oracledb [{:key "host" :label "Host" :value "" :required true}
              {:key "user" :label "User" :value "" :required true}
              {:key "pass" :label "Pass" :value "" :required true}
              {:key "port" :label "Port" :value "" :required true}
              {:key "sid" :placeholder "SID or Service name" :label "SID" :value "" :required true}]
   :mongodb [{:key "connection_string"
              :label "Connection string"
              :value ""
              :required true
              :placeholder "mongodb+srv://root:<password>@devcluster.mwb5sun.mongodb.net/"}]
   :ssh [{:key "host" :label "Host" :value "" :required true}
         {:key "port" :label "Port" :value "" :required false}
         {:key "user" :label "User" :value "" :required true}
         {:key "pass" :label "Pass" :value "" :required true}
         {:key "authorized_server_keys"
          :label "Private Key"
          :value ""
          :required true
          :placeholder "Enter your private key"
          :type "textarea"}]})


(def connection-icons-rounded-dictionary
  {:postgres (str config/webapp-url "/icons/connections/postgres-rounded.svg")
   :postgres-csv (str config/webapp-url "/icons/connections/postgres-rounded.svg")
   :command-line (str config/webapp-url "/icons/connections/custom-ssh.svg")
   :ssh (str config/webapp-url "/icons/connections/custom-ssh.svg")
   :custom (str config/webapp-url "/icons/connections/custom-ssh.svg")
   :tcp (str config/webapp-url "/icons/connections/custom-tcp-http.svg")
   :rdp (str config/webapp-url "/icons/connections/custom-tcp-http.svg")
   :httpproxy (str config/webapp-url "/icons/connections/custom-tcp-http.svg")
   :claude-code (str config/webapp-url "/icons/connections/claude-default.svg")
   :mysql (str config/webapp-url "/icons/connections/mysql-rounded.svg")
   :mysql-csv (str config/webapp-url "/icons/connections/mysql-rounded.svg")
   :aws (str config/webapp-url "/icons/connections/aws-rounded.svg")
   :awscli (str config/webapp-url "/icons/connections/awscli-rounded.svg")
   :nodejs (str config/webapp-url "/icons/connections/node-rounded.svg")
   :python (str config/webapp-url "/icons/connections/python-rounded.svg")
   :ruby-on-rails (str config/webapp-url "/icons/connections/rails-rounded.svg")
   :clojure (str config/webapp-url "/icons/connections/clojure-rounded.svg")
   :kubernetes (str config/webapp-url "/icons/connections/kubernetes-rounded.svg")
   :sql-server-csv (str config/webapp-url "/icons/connections/mssql-rounded.svg")
   :sql-server (str config/webapp-url "/icons/connections/mssql-rounded.svg")
   :oracledb (str config/webapp-url "/icons/connections/oracle-rounded.svg")
   :mssql (str config/webapp-url "/icons/connections/mssql-rounded.svg")
   :mongodb (str config/webapp-url "/icons/connections/mongodb-rounded.svg")
   :npm (str config/webapp-url "/icons/connections/npm-rounded.svg")
   :yarn (str config/webapp-url "/icons/connections/yarn-rounded.svg")
   :docker (str config/webapp-url "/icons/connections/docker-rounded.svg")
   :googlecloud (str config/webapp-url "/icons/connections/googlecloud-rounded.svg")
   :helm (str config/webapp-url "/icons/connections/helm-rounded.svg")
   :git (str config/webapp-url "/icons/connections/git-rounded.svg")
   :github (str config/webapp-url "/icons/connections/github-default.svg")
   :sentry (str config/webapp-url "/icons/connections/sentry-rounded.svg")
   :django (str config/webapp-url "/icons/connections/django-rounded.svg")
   :elixir (str config/webapp-url "/icons/connections/elixir-rounded.svg")
   :cloudwatch (str config/webapp-url "/icons/connections/aws-cloudwatch-default.svg")
   :dynamodb (str config/webapp-url "/icons/connections/aws-dynamodb-default.svg")
   :bigquery (str config/webapp-url "/icons/connections/google-bigquery-default.svg")
   :laravel (str config/webapp-url "/icons/connections/laravel-default.svg")
   :cassandra (str config/webapp-url "/icons/connections/cassandra-default.svg")
   :redis (str config/webapp-url "/icons/connections/redis-default.svg")
   :kubernetes-admin (str config/webapp-url "/icons/connections/kubernetes-rounded.svg")
   :kubernetes-exec (str config/webapp-url "/icons/connections/kubernetes-rounded.svg")
   :kubernetes-interactive (str config/webapp-url "/icons/connections/kubernetes-rounded.svg")
   :kubernetes-token (str config/webapp-url "/icons/connections/kubernetes-rounded.svg")
   :aws-cli (str config/webapp-url "/icons/connections/aws-rounded.svg")
   :aws-ecs (str config/webapp-url "/icons/connections/aws-rounded.svg")
   :aws-ssm (str config/webapp-url "/icons/connections/aws-rounded.svg")
   :grafana (str config/webapp-url "/icons/connections/grafana-rounded.svg")
   :kibana (str config/webapp-url "/icons/connections/kibana-rounded.svg")})

(def connection-icons-default-dictionary
  {:postgres (str config/webapp-url "/icons/connections/postgres-default.svg")
   :postgres-csv (str config/webapp-url "/icons/connections/postgres-default.svg")
   :command-line (str config/webapp-url "/icons/connections/custom-ssh.svg")
   :ssh (str config/webapp-url "/icons/connections/custom-ssh.svg")
   :custom (str config/webapp-url "/icons/connections/custom-ssh.svg")
   :tcp (str config/webapp-url "/icons/connections/custom-tcp-http.svg")
   :rdp (str config/webapp-url "/icons/connections/custom-tcp-http.svg")
   :httpproxy (str config/webapp-url "/icons/connections/custom-tcp-http.svg")
   :claude-code (str config/webapp-url "/icons/connections/claude-default.svg")
   :mysql (str config/webapp-url "/icons/connections/mysql-default.svg")
   :mysql-csv (str config/webapp-url "/icons/connections/mysql-default.svg")
   :aws (str config/webapp-url "/icons/connections/aws-default.svg")
   :awscli (str config/webapp-url "/icons/connections/awscli-default.svg")
   :nodejs (str config/webapp-url "/icons/connections/node-default.svg")
   :python (str config/webapp-url "/icons/connections/python-default.svg")
   :ruby-on-rails (str config/webapp-url "/icons/connections/rails-default.svg")
   :clojure (str config/webapp-url "/icons/connections/clojure-default.svg")
   :kubernetes (str config/webapp-url "/icons/connections/kubernetes-default.svg")
   :sql-server-csv (str config/webapp-url "/icons/connections/mssql-default.svg")
   :sql-server (str config/webapp-url "/icons/connections/mssql-default.svg")
   :oracledb (str config/webapp-url "/icons/connections/oracle-default.svg")
   :mssql (str config/webapp-url "/icons/connections/mssql-default.svg")
   :mongodb (str config/webapp-url "/icons/connections/mongodb-default.svg")
   :npm (str config/webapp-url "/icons/connections/npm-default.svg")
   :yarn (str config/webapp-url "/icons/connections/yarn-default.svg")
   :docker (str config/webapp-url "/icons/connections/docker-default.svg")
   :googlecloud (str config/webapp-url "/icons/connections/googlecloud-default.svg")
   :helm (str config/webapp-url "/icons/connections/helm-default.svg")
   :git (str config/webapp-url "/icons/connections/git-default.svg")
   :github (str config/webapp-url "/icons/connections/github-default.svg")
   :sentry (str config/webapp-url "/icons/connections/sentry-default.svg")
   :django (str config/webapp-url "/icons/connections/django-default.svg")
   :elixir (str config/webapp-url "/icons/connections/elixir-default.svg")
   :cloudwatch (str config/webapp-url "/icons/connections/aws-cloudwatch-default.svg")
   :dynamodb (str config/webapp-url "/icons/connections/aws-dynamodb-default.svg")
   :bigquery (str config/webapp-url "/icons/connections/google-bigquery-default.svg")
   :laravel (str config/webapp-url "/icons/connections/laravel-default.svg")
   :cassandra (str config/webapp-url "/icons/connections/cassandra-default.svg")
   :redis (str config/webapp-url "/icons/connections/redis-default.svg")
   :kubernetes-admin (str config/webapp-url "/icons/connections/kubernetes-default.svg")
   :kubernetes-exec (str config/webapp-url "/icons/connections/kubernetes-default.svg")
   :kubernetes-interactive (str config/webapp-url "/icons/connections/kubernetes-default.svg")
   :kubernetes-token (str config/webapp-url "/icons/connections/kubernetes-default.svg")
   :aws-cli (str config/webapp-url "/icons/connections/aws-default.svg")
   :aws-ecs (str config/webapp-url "/icons/connections/aws-default.svg")
   :aws-ssm (str config/webapp-url "/icons/connections/aws-default.svg")
   :grafana (str config/webapp-url "/icons/connections/grafana-default.svg")
   :kibana (str config/webapp-url "/icons/connections/kibana-default.svg")})

(def command-to-icon-key
  {"aws" :aws
   "clj" :clojure
   "docker" :docker
   "docker-compose" :docker
   "gcloud" :googlecloud
   "git" :git
   "github" :github
   "helm" :helm
   "kubectl" :kubernetes
   "mongosh" :mongodb
   "mssql" :mssql
   "mysql" :mysql
   "node" :nodejs
   "npm" :npm
   "oci" :oracledb
   "psql" :postgres
   "python" :python
   "python3" :python
   "rails" :ruby-on-rails
   "sentry-cli" :sentry
   "yarn" :yarn
   "ssh" :ssh
   "bash" :custom
   "php" :laravel
   "cqlsh" :apache-cassandra
   "redis-cli" :redis})

(defn get-connection-icon [connection & [icon-style]]
  (let [icon-key (cond
                   (not (cs/blank? (:subtype connection))) (keyword (:subtype connection))
                   (and (= "custom" (:type connection)) (not (cs/blank? (:command connection))))
                   (let [command-first-term (first (:command connection))]
                     (get command-to-icon-key command-first-term :custom))
                   (not (cs/blank? (:icon_name connection))) (keyword (:icon_name connection))
                   (not (cs/blank? (:type connection))) (keyword (:type connection))
                   :else :custom)]

    (cond
      (= icon-style "rounded") (get connection-icons-rounded-dictionary icon-key
                                    (str config/webapp-url "/icons/connections/custom-ssh.svg"))
      (= icon-style "default") (get connection-icons-default-dictionary icon-key
                                    (str config/webapp-url "/icons/connections/custom-ssh.svg"))
      :else (get connection-icons-default-dictionary icon-key
                 (str config/webapp-url "/icons/connections/custom-ssh.svg")))))

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
   "ssh" "ssh -t -o StrictHostKeyChecking=accept-new $SSH_URI -i $SSH_PRIVATE_KEY bash"
   "oracledb" ""
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
   :redact_enabled true
   :redact_types []
   :secret {:envvar:DB "ZGVsbHN0b3Jl"
            :envvar:HOST "ZGVtby1wZy1kYi5jaDcwN3JuYWl6amcudXMtZWFzdC0xLnJkcy5hbWF6b25hd3MuY29t"
            :envvar:INSECURE "ZmFsc2U="
            :envvar:PASS "ZG9sbGFyLW1hbmdlci1jYXJvdXNlLUhFQVJURUQ="
            :envvar:PORT "NTQzMg=="
            :envvar:USER "ZGVtb3JlYWRvbmx5"}
   :command []})
