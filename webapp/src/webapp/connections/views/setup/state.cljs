(ns webapp.connections.views.setup.state
  (:require [reagent.core :as r]))

;; Connection type definitions matching UI requirements
(def connection-types
  [{:id "database"
    :title "Database"
    :description "Connect to databases like MySQL, PostgreSQL and more."
    :icon "database"}
   {:id "server-container"
    :title "Linux VM or Container"
    :description "Set up access to your Linux servers, Docker and Kubernetes."
    :icon "container"}
   {:id "network"
    :title "Network service access (ZTNA)"
    :description "Securely connect to internal network resources."
    :icon "network"}])

(def database-types
  [{:id "postgres" :title "PostgreSQL"}
   {:id "mysql" :title "MySQL"}
   {:id "mssql" :title "Microsoft SQL"}
   {:id "oracledb" :title "Oracle DB"}
   {:id "mongodb" :title "MongoDB"}])

(def server-types
  [{:id "ssh"
    :title "Linux VM or Container"
    :description "Secure shell protocol (SSH) for remote access."}
   {:id "console"
    :title "Console"
    :description "For Ruby on Rails, Python, Node JS and more."}])

(def application-types
  [{:id "ruby-on-rails" :title "Ruby on Rails"}
   {:id "python" :title "Python"}
   {:id "nodejs" :title "Node.js"}
   {:id "clojure" :title "Clojure"}])

(def operation-systems
  [{:id "macos" :title "MacOS"}
   {:id "linux" :title "Linux"}])

(def network-types
  [{:id "tcp" :title "TCP"}
   {:id "http" :title "HTTP"}])

(defn get-subtypes [connection-type]
  (case connection-type
    "database" database-types
    "custom" server-types
    "application" network-types
    []))

(defn create-initial-state [form-type initial-data]
  {:step :resource-type
   :connection-type (r/atom (or (:type initial-data) nil))
   :connection-subtype (r/atom (or (:subtype initial-data) nil))
   :connection-name (r/atom (or (:name initial-data) ""))
   :app-type (r/atom nil)  ;; Para quando console é selecionado
   :credentials (r/atom {})
   :environment-variables (r/atom [])  ;; Para SSH/Console
   :configuration-files (r/atom [])    ;; Para SSH/Console
   :command (r/atom nil)               ;; Para comando adicional
   :form-type form-type})
