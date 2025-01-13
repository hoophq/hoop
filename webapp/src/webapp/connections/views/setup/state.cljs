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

(def server-container-types
  [{:id "linux-vm" :title "Linux VM or Container"
    :description "Secure shell protocol (SSH) for remote access."}
   {:id "console" :title "Console"
    :description "For Ruby on Rails, Python, Node JS and more."}])

(def network-types
  [{:id "tcp" :title "TCP"}
   {:id "http" :title "HTTP"}])

(defn create-initial-state [form-type initial-data]
  (let [credentials (select-keys initial-data [:host :user :pass :port :database :ssl-mode])]
    {:step :resource-type
     :connection-type (r/atom (or (:type initial-data) nil))
     :connection-subtype (r/atom (or (:subtype initial-data) nil))
     :connection-name (r/atom (or (:name initial-data) ""))
     :credentials (r/atom credentials)
     :form-type form-type}))

(defn get-subtypes [connection-type]
  (case connection-type
    "database" database-types
    "server-container" server-container-types
    "network" network-types
    []))
